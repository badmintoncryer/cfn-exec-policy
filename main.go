// Command cfn-exec-policy generates the IAM policy a CloudFormation execution role
// needs for your templates, so CDK bootstrap doesn't have to use AdministratorAccess.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

var version = "dev"

const usage = `cfn-exec-policy — IAM policy for your CloudFormation execution role

Usage:
  cfn-exec-policy generate [flags] [cdk.out | template ...]   print the policy document(s)
  cfn-exec-policy check    [flags] [cdk.out | template ...]   exit 1 if the deployed policy is missing actions
  cfn-exec-policy apply    [flags] [cdk.out | template ...]   create/update the managed policy and print the bootstrap command
  cfn-exec-policy delete   [--policy-name n] [--yes]          delete the managed policy (dry run without --yes)
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
	case "delete":
		err = runDelete(os.Args[2:])
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
	passCond   *bool
	strict     *bool
	boundary   *string
}

func newFlags(name string) common {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	return common{
		fs:         fs,
		refresh:    fs.Bool("refresh-schemas", false, "use the latest CloudFormation schemas instead of the embedded table"),
		policyName: fs.String("policy-name", "cfn-exec-policy", "managed policy name (extra documents get -2, -3, …)"),
		passCond:   fs.Bool("pass-role-condition", false, "limit iam:PassRole with iam:PassedToService to the services the templates pass roles to"),
		strict:     fs.Bool("strict", false, "require a permissions boundary on every role and scope iam:PassRole to the stacks' roles (implies --pass-role-condition)"),
		boundary:   fs.String("boundary", "", "with --strict, your own permissions boundary (policy name or ARN) instead of <policy-name>-boundary"),
	}
}

// required loads inputs (plus deployed templates when clients is set) and returns
// the needed actions, printing warnings to stderr. With --strict it also returns
// the PassRole scope and errors on anything the boundary would reject.
func required(c common, clients *awsClients) ([]string, *Strict, []*Template, error) {
	tbl, err := LoadTable(*c.refresh)
	if err != nil {
		return nil, nil, nil, err
	}
	tpls, err := LoadInputs(c.fs.Args())
	if err != nil {
		return nil, nil, nil, err
	}
	inputs := tpls
	var strict *Strict
	var errs []string
	if *c.strict {
		account := ""
		if clients != nil {
			account = clients.account
		}
		name, err := boundaryName(boundaryOrDefault(c), account)
		if err != nil {
			return nil, nil, nil, err
		}
		strict = &Strict{Boundary: name}
		// Only the templates about to be deployed: the roles already deployed get the boundary with them.
		errs = StrictErrors(tpls, name)
	}
	if clients != nil {
		deployed, err := clients.deployedTemplates(context.Background(), tpls)
		if err != nil {
			return nil, nil, nil, err
		}
		tpls = append(tpls, deployed...)
	}
	actions, warnings, guessed := RequiredActions(tbl, tpls, *c.passCond || *c.strict)
	if strict != nil {
		for _, typ := range guessed {
			errs = append(errs, typ+" has no measured permissions, which --strict does not guess; please open an issue")
		}
		roles, ws, err := PassRoleScope(tpls)
		if err != nil {
			return nil, nil, nil, err
		}
		strict.Roles = roles
		warnings = append(warnings, ws...)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if len(errs) > 0 {
		return nil, nil, nil, fmt.Errorf("--strict:\n  %s", strings.Join(errs, "\n  "))
	}
	return actions, strict, inputs, nil
}

// mergeRoles returns the sorted union of two role pattern lists.
func mergeRoles(a, b []string) []string {
	out := slices.Clone(a)
	for _, r := range b {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

func boundaryOrDefault(c common) string {
	if *c.boundary != "" {
		return *c.boundary
	}
	return *c.policyName + "-boundary"
}

func runGenerate(args []string) error {
	c := newFlags("generate")
	c.fs.Parse(args)
	actions, strict, _, err := required(c, nil)
	if err != nil {
		return err
	}
	if strict != nil && *c.boundary == "" {
		fmt.Fprintf(os.Stderr, "note: `apply --strict` also writes the permissions boundary %s\n", strict.Boundary)
	}
	docs := Documents(Compact(actions), strict)
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
	need, strict, _, err := required(c, clients)
	if err != nil {
		return err
	}
	have, haveRoles, parts, err := clients.existingActions(ctx, *c.policyName)
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
	if strict != nil {
		for _, r := range strict.Roles {
			if !slices.Contains(haveRoles, r) {
				missing = append(missing, "iam:PassRole on "+r)
			}
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
	actions, strict, inputs, err := required(c, clients)
	if err != nil {
		return err
	}
	have, haveRoles, parts, err := clients.existingActions(ctx, *c.policyName)
	if err != nil {
		return err
	}
	if !*prune {
		// The exec role is shared by every app bootstrapped in this environment,
		// so keep what other apps needed, including the roles they pass.
		actions = append(actions, have...)
		if strict != nil {
			strict.Roles = mergeRoles(strict.Roles, haveRoles)
		}
	}
	if ws := Wildcards(actions); strict != nil && len(ws) > 0 {
		return fmt.Errorf("--strict: %s in %s would grant iam:PassRole and iam:CreateRole on every role; "+
			"remove it from the policy, or re-run with --prune", strings.Join(ws, ", "), *c.policyName)
	}
	docs := Documents(Compact(actions), strict)
	// Parts that are no longer needed stay attached to the exec role until it is
	// re-bootstrapped, so empty them rather than leave their old grants in place.
	for len(docs) < parts {
		docs = append(docs, EmptyDocument())
	}
	if strict != nil && *c.boundary == "" {
		b := BoundaryDocument(clients.part, clients.account, strict.Boundary, *c.policyName)
		if err := clients.putPolicy(ctx, strict.Boundary, b); err != nil {
			return fmt.Errorf("%s: %w", strict.Boundary, err)
		}
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
	opts := " --cloudformation-execution-policies " + strings.Join(arns, ",")
	if strict != nil {
		opts += " --custom-permissions-boundary " + strict.Boundary
	}
	clients.printBootstrap(ctx, execRoles(inputs, clients), opts)
	if strict != nil {
		fmt.Fprintf(os.Stderr, "\nEvery app deploying to this environment needs the boundary on its roles; in cdk.json:\n"+
			"  \"context\": {\"@aws-cdk/core:permissionsBoundary\": {\"name\": %q}}\n", strict.Boundary)
	}
	return nil
}

func runDelete(args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	policyName := fs.String("policy-name", "cfn-exec-policy", "managed policy name (-2, -3, … and the --strict boundary <policy-name>-boundary are deleted too)")
	yes := fs.Bool("yes", false, "delete; without it, only print what would be deleted")
	fs.Parse(args)
	ctx := context.Background()
	clients, err := newClients(ctx)
	if err != nil {
		return err
	}
	_, _, parts, err := clients.existingActions(ctx, *policyName)
	if err != nil {
		return err
	}
	names := policyNames(*policyName, parts)
	// Only the boundary `apply --strict` wrote; one passed with --boundary is not ours.
	boundary := *policyName + "-boundary"
	hasBoundary, err := clients.exists(ctx, boundary)
	if err != nil {
		return err
	}
	if parts == 0 && !hasBoundary {
		return fmt.Errorf("policy %s not found", clients.policyArn(*policyName))
	}
	if parts == 0 {
		names = nil
	}
	attached := false
	var roles []string
	for _, n := range names {
		es, err := clients.attachedTo(ctx, n)
		if err != nil {
			return err
		}
		for _, e := range es {
			if r, ok := strings.CutPrefix(e, "role/"); ok && !slices.Contains(roles, r) {
				roles = append(roles, r)
			}
		}
		if len(es) > 0 {
			attached = true
			fmt.Fprintf(os.Stderr, "%s is attached to %s\n", n, strings.Join(es, ", "))
		}
	}
	var bounded []string
	if hasBoundary {
		if bounded, err = clients.attachedTo(ctx, boundary); err != nil {
			return err
		}
		if len(bounded) > 0 {
			fmt.Fprintf(os.Stderr, "%s is the permissions boundary of %s\n", boundary, strings.Join(bounded, ", "))
		}
	}
	if attached {
		// Detaching it here would leave the bootstrap stack pointing at a deleted
		// policy, so let CloudFormation do it.
		fmt.Fprintf(os.Stderr, "\nPoint the execution role back at AdministratorAccess (or delete the bootstrap stack) first:\n\n")
		clients.printBootstrap(ctx, roles, " --cloudformation-execution-policies arn:"+clients.part+":iam::aws:policy/AdministratorAccess")
	}
	// Not for the boundary alone: re-bootstrapping doesn't take it off the exec role.
	if len(bounded) > 0 {
		fmt.Fprint(os.Stderr, boundaryRemoval(boundary, bounded))
	}
	if attached || len(bounded) > 0 {
		os.Exit(1)
	}
	if hasBoundary {
		// Deleted first, so a failure leaves the base for a re-run to find.
		names = append(names, boundary)
	}
	if !*yes {
		fmt.Fprintf(os.Stderr, "would delete %s\nre-run with --yes to delete\n", strings.Join(names, ", "))
		return nil
	}
	// Base goes last: parts are found by walking from it, so a re-run after a
	// partial failure still finds the rest.
	for i := len(names) - 1; i >= 0; i-- {
		n := names[i]
		if err := clients.deletePolicy(ctx, n); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	fmt.Fprintf(os.Stderr, "deleted %s\n", strings.Join(names, ", "))
	return nil
}

// boundaryRemoval says how to take the boundary off the roles that carry it. The exec
// role's goes first: the boundary denies removing boundaries, so while the exec role
// has it, no deploy can take it off the other roles.
func boundaryRemoval(boundary string, roles []string) string {
	var exec, others []string
	for _, r := range roles {
		if strings.HasPrefix(r, "role/cdk-") && strings.Contains(r, "-cfn-exec-role-") {
			exec = append(exec, r)
		} else {
			others = append(others, r)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nIAM won't delete %s while roles carry it. Remove it from them, in this order:\n", boundary)
	if len(exec) > 0 {
		fmt.Fprintf(&b, "  - %s: `cdk bootstrap` keeps the boundary when --custom-permissions-boundary is left out,\n"+
			"    even though it prints \"Removing existing permissions boundary\". Clear the\n"+
			"    InputPermissionsBoundary parameter of the bootstrap stack instead (CloudFormation\n"+
			"    console: Update stack, Use existing template), or delete the bootstrap stack.\n", strings.Join(exec, ", "))
	}
	if len(others) > 0 {
		fmt.Fprintf(&b, "  - %s: remove \"@aws-cdk/core:permissionsBoundary\" from cdk.json and redeploy\n"+
			"    (or destroy) each app.\n", strings.Join(others, ", "))
	}
	return b.String()
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
