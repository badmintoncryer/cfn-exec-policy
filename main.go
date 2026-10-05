// Command cfn-exec-policy generates the IAM policy a CloudFormation execution role
// needs for your templates, so CDK bootstrap doesn't have to use AdministratorAccess.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

var version = "dev"

const usage = `cfn-exec-policy — IAM policy for your CloudFormation execution role

Usage:
  cfn-exec-policy generate [flags] [cdk.out | template ...]   print the policy document(s)
  cfn-exec-policy check    [flags] [cdk.out | template ...]   exit 1 if the deployed policy is missing actions
  cfn-exec-policy apply    [flags] [cdk.out | template ...]   create/update the managed policy and print the bootstrap command
  cfn-exec-policy version

Inputs default to ./cdk.out. check and apply also include the currently deployed
templates, so resources you remove keep their delete permissions.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "generate":
		err = runGenerate(os.Args[2:])
	case "check":
		err = runCheck(os.Args[2:])
	case "apply":
		err = runApply(os.Args[2:])
	case "table": // maintainer: regenerate table.json from a schema bundle
		err = runTable(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type common struct {
	fs         *flag.FlagSet
	refresh    *bool
	policyName *string
}

func newFlags(name string) common {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	return common{
		fs:         fs,
		refresh:    fs.Bool("refresh-schemas", false, "use the latest CloudFormation schemas instead of the embedded table"),
		policyName: fs.String("policy-name", "cfn-exec-policy", "managed policy name (extra documents get -2, -3, …)"),
	}
}

// required loads inputs (plus deployed templates when clients is set) and returns
// the needed actions, printing warnings to stderr.
func required(c common, clients *awsClients) ([]string, error) {
	tbl, err := LoadTable(*c.refresh)
	if err != nil {
		return nil, err
	}
	tpls, err := LoadInputs(c.fs.Args())
	if err != nil {
		return nil, err
	}
	if clients != nil {
		deployed, err := clients.deployedTemplates(context.Background(), tpls)
		if err != nil {
			return nil, err
		}
		tpls = append(tpls, deployed...)
	}
	actions, warnings := RequiredActions(tbl, tpls)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return actions, nil
}

func runGenerate(args []string) error {
	c := newFlags("generate")
	c.fs.Parse(args)
	actions, err := required(c, nil)
	if err != nil {
		return err
	}
	docs := Documents(Compact(actions))
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if len(docs) == 1 {
		return enc.Encode(docs[0])
	}
	fmt.Fprintf(os.Stderr, "note: %d documents (IAM limit is %d characters each); attach all of them\n", len(docs), MaxPolicySize)
	return enc.Encode(docs)
}

func runCheck(args []string) error {
	c := newFlags("check")
	c.fs.Parse(args)
	ctx := context.Background()
	clients, err := newClients(ctx)
	if err != nil {
		return err
	}
	need, err := required(c, clients)
	if err != nil {
		return err
	}
	have, parts, err := clients.existingActions(ctx, *c.policyName)
	if err != nil {
		return err
	}
	if parts == 0 {
		return fmt.Errorf("policy %s not found; run `cfn-exec-policy apply` first", clients.policyArn(*c.policyName))
	}
	var missing []string
	for _, a := range need {
		if !Covered(a, have) {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s is missing %d action(s):\n  %s\nrun `cfn-exec-policy apply` to add them\n",
			*c.policyName, len(missing), strings.Join(missing, "\n  "))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s* covers all %d required actions\n", *c.policyName, len(need))
	return nil
}

func runApply(args []string) error {
	c := newFlags("apply")
	prune := c.fs.Bool("prune", false, "drop actions no longer needed (other apps sharing this bootstrap may break)")
	c.fs.Parse(args)
	ctx := context.Background()
	clients, err := newClients(ctx)
	if err != nil {
		return err
	}
	actions, err := required(c, clients)
	if err != nil {
		return err
	}
	have, parts, err := clients.existingActions(ctx, *c.policyName)
	if err != nil {
		return err
	}
	if !*prune {
		// The exec role is shared by every app bootstrapped in this environment,
		// so keep what other apps needed.
		actions = append(actions, have...)
	}
	docs := Documents(Compact(actions))
	// Parts that are no longer needed stay attached to the exec role until it is
	// re-bootstrapped, so empty them rather than leave their old grants in place.
	for len(docs) < parts {
		docs = append(docs, EmptyDocument())
	}
	names := policyNames(*c.policyName, len(docs))
	var arns []string
	for i, d := range docs {
		if err := clients.putPolicy(ctx, names[i], d); err != nil {
			return fmt.Errorf("%s: %w", names[i], err)
		}
		arns = append(arns, clients.policyArn(names[i]))
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n\nNow point the execution role at it:\n\n", strings.Join(names, ", "))
	fmt.Printf("cdk bootstrap aws://%s/%s --cloudformation-execution-policies %s\n",
		clients.account, clients.cfg.Region, strings.Join(arns, ","))
	return nil
}

func runTable(args []string) error {
	fs := flag.NewFlagSet("table", flag.ExitOnError)
	zipPath := fs.String("zip", "", "schema bundle zip (default: download)")
	out := fs.String("out", "table.json", "output file")
	fs.Parse(args)
	var zb []byte
	var err error
	if *zipPath != "" {
		zb, err = os.ReadFile(*zipPath)
	} else {
		zb, err = fetchSchemaZip()
	}
	if err != nil {
		return err
	}
	t, err := BuildTable(zb)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", " ")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d types, %d namespaces\n", len(t.Types), len(t.Prefixes))
	return os.WriteFile(*out, append(b, '\n'), 0o644)
}
