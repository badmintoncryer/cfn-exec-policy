package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// SchemaURL is the CloudFormation resource provider schema bundle.
const SchemaURL = "https://schema.cloudformation.us-east-1.amazonaws.com/CloudformationSchema.zip"

// Table maps resource types to the IAM actions their handlers call.
type Table struct {
	// Types: union of create/read/update/delete handler permissions.
	Types map[string][]string `json:"types"`
	// Prefixes: IAM service prefix per type namespace ("AWS::CodeBuild" -> "codebuild"),
	// used to fall back to "<prefix>:*" for types without handlers.
	Prefixes map[string]string `json:"prefixes"`
}

//go:embed table.json
var embeddedTable []byte

// Prefixes that can't be derived from handler permissions (no handled types, or
// the service name differs from its IAM prefix). These win over derived ones.
var knownPrefixes = map[string]string{
	"AWS::EMR":                  "elasticmapreduce",
	"AWS::Greengrass":           "greengrass",
	"AWS::AppMesh":              "appmesh",
	"AWS::WAF":                  "waf",
	"AWS::CodeGuruReviewer":     "codeguru-reviewer",
	"AWS::ComputeOptimizer":     "compute-optimizer",
	"AWS::S3ObjectLambda":       "s3-object-lambda",
	"AWS::SSMQuickSetup":        "ssm-quicksetup",
	"AWS::ElasticLoadBalancing": "elasticloadbalancing",
	"AWS::WAFRegional":          "waf-regional",
	"AWS::Pinpoint":             "mobiletargeting",
	"AWS::PinpointEmail":        "ses",
	"AWS::SDB":                  "sdb",
}

func namespace(typeName string) string {
	if i := strings.LastIndex(typeName, "::"); i > 0 {
		return typeName[:i]
	}
	return typeName
}

// BuildTable extracts a Table from the schema bundle zip.
func BuildTable(zipBytes []byte) (*Table, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	t := &Table{Types: map[string][]string{}, Prefixes: map[string]string{}}
	counts := map[string]map[string]int{} // namespace -> action prefix -> count
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		var s struct {
			TypeName string `json:"typeName"`
			Handlers map[string]struct {
				Permissions []string `json:"permissions"`
			} `json:"handlers"`
		}
		err = json.NewDecoder(rc).Decode(&s)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		set := map[string]bool{}
		for _, h := range []string{"create", "read", "update", "delete"} {
			for _, p := range s.Handlers[h].Permissions {
				set[p] = true
			}
		}
		if len(set) == 0 {
			continue
		}
		t.Types[s.TypeName] = sortedKeys(set)
		ns := namespace(s.TypeName)
		if counts[ns] == nil {
			counts[ns] = map[string]int{}
		}
		for a := range set {
			if p, _, ok := strings.Cut(a, ":"); ok {
				counts[ns][strings.ToLower(p)]++
			}
		}
	}
	for ns, c := range counts {
		t.Prefixes[ns] = pickPrefix(ns, c)
	}
	for ns, p := range knownPrefixes {
		t.Prefixes[ns] = p
	}
	return t, nil
}

// pickPrefix prefers the prefix named like the service, then the most common one
// that isn't a cross-cutting service (iam, kms, ec2 for networking, …).
func pickPrefix(ns string, counts map[string]int) string {
	svc := strings.ToLower(ns[strings.LastIndex(ns, ":")+1:])
	if counts[svc] > 0 {
		return svc
	}
	best, n := "", 0
	for p, k := range counts {
		if crossCutting[p] {
			continue
		}
		if k > n || (k == n && p < best) {
			best, n = p, k
		}
	}
	return best
}

var crossCutting = map[string]bool{"iam": true, "kms": true, "ec2": true, "s3": true, "logs": true,
	"sso": true, "tag": true, "secretsmanager": true, "ssm": true, "cloudwatch": true, "sns": true, "lambda": true}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fetchSchemaZip() ([]byte, error) {
	resp, err := http.Get(SchemaURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", SchemaURL, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// LoadTable returns the embedded table, or one built from the latest schema
// bundle when refresh is set.
func LoadTable(refresh bool) (*Table, error) {
	if refresh {
		zb, err := fetchSchemaZip()
		if err != nil {
			return nil, err
		}
		return BuildTable(zb)
	}
	var t Table
	if err := json.Unmarshal(embeddedTable, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
