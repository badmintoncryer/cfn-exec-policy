package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Risks a diff can flag, for --fail-on.
const (
	riskWildcard = "wildcard" // a <service>:* action, from a type with no measured permissions
	riskPassRole = "passrole" // iam:PassRole with no iam:PassedToService condition
	riskUnknown  = "unknown"  // a type with no permissions at all
)

// TypeChange is what one resource type adds to or drops from the policy.
type TypeChange struct {
	Type    string
	New     bool     // the type is not in the base templates
	Gone    bool     // the type is only in the base templates
	Actions []string // added (or, for a type that is gone, dropped) actions
	Risks   []string
}

// Diff is how the policy of the head templates differs from the base's.
type Diff struct {
	Added    []TypeChange // types that add actions, or that are new
	Removed  []TypeChange // types that are gone, with the actions only they needed
	Dropped  []string     // actions the base needs and the head does not
	NewRoles []string     // --strict: role patterns iam:PassRole reaches that the base does not
	Risks    []string     // the kinds of risk in Added, sorted
}

// Empty reports whether the head needs nothing the base doesn't, and drops nothing.
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Dropped) == 0 && len(d.NewRoles) == 0
}

// DiffPolicies compares what two sets of templates need. An action is added when
// nothing the base needs covers it, so a type that moves from the <service>:*
// fallback to measured permissions adds nothing.
func DiffPolicies(tbl *Table, base, head []*Template, passCond bool) Diff {
	baseBy, _, baseGuessed := RequiredActionsBy(tbl, base, passCond)
	headBy, _, headGuessed := RequiredActionsBy(tbl, head, passCond)
	baseAll, headAll := union(baseBy), union(headBy)
	var d Diff
	risks := map[string]bool{}
	for _, typ := range sortedTypes(headBy, headGuessed) {
		_, inBase := baseBy[typ]
		inBase = inBase || slices.Contains(baseGuessed, typ)
		c := TypeChange{Type: typ, New: !inBase}
		for _, a := range headBy[typ] {
			if !Covered(a, baseAll) {
				c.Actions = append(c.Actions, a)
			}
		}
		for _, a := range c.Actions {
			switch {
			case a == "iam:PassRole":
				c.Risks = appendNew(c.Risks, riskPassRole)
			case strings.HasSuffix(a, ":*"):
				c.Risks = appendNew(c.Risks, riskWildcard)
			}
		}
		if c.New && slices.Contains(headGuessed, typ) && len(headBy[typ]) == 0 {
			c.Risks = appendNew(c.Risks, riskUnknown)
		}
		if len(c.Actions) > 0 || c.New && typ != dynamicRefs && typ != "AWS::CDK::Metadata" {
			d.Added = append(d.Added, c)
			for _, r := range c.Risks {
				risks[r] = true
			}
		}
	}
	for _, a := range baseAll {
		if !Covered(a, headAll) {
			d.Dropped = append(d.Dropped, a)
		}
	}
	for _, typ := range sortedTypes(baseBy, baseGuessed) {
		_, inHead := headBy[typ]
		if inHead || slices.Contains(headGuessed, typ) || typ == dynamicRefs {
			continue
		}
		c := TypeChange{Type: typ, Gone: true}
		for _, a := range baseBy[typ] {
			if slices.Contains(d.Dropped, a) {
				c.Actions = append(c.Actions, a)
			}
		}
		d.Removed = append(d.Removed, c)
	}
	d.Risks = sortedKeys(risks)
	return d
}

func union(by map[string][]string) []string {
	set := map[string]bool{}
	for _, as := range by {
		for _, a := range as {
			set[a] = true
		}
	}
	return sortedKeys(set)
}

// sortedTypes lists the sources of by and the guessed types, resource types first.
func sortedTypes(by map[string][]string, guessed []string) []string {
	set := map[string]bool{}
	for k := range by {
		set[k] = true
	}
	for _, g := range guessed {
		set[g] = true
	}
	delete(set, "AWS::CDK::Metadata")
	out := sortedKeys(set)
	if i := slices.Index(out, dynamicRefs); i >= 0 {
		out = append(slices.Delete(out, i, i+1), dynamicRefs)
	}
	return out
}

func appendNew(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

// displayAction spells a passRole token for people.
func displayAction(a string) string {
	if svc, ok := strings.CutPrefix(a, passRole); ok {
		return "iam:PassRole to " + svc
	}
	return a
}

var riskText = map[string]string{
	riskWildcard: "grants every action of the service: the type has no measured permissions",
	riskPassRole: "iam:PassRole on any role, with no iam:PassedToService condition",
	riskUnknown:  "unknown type: no permissions granted, so its deploy fails",
}

func (d Diff) addedActions() int {
	n := 0
	for _, c := range d.Added {
		n += len(c.Actions)
	}
	return n
}

func (d Diff) summary() string {
	n, m := d.addedActions(), len(d.Dropped)
	s := fmt.Sprintf("+%d action%s", n, plural(n))
	if m > 0 {
		s += fmt.Sprintf(" · -%d no longer needed", m)
	}
	if len(d.NewRoles) > 0 {
		s += fmt.Sprintf(", +%d PassRole role pattern%s", len(d.NewRoles), plural(len(d.NewRoles)))
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func typeLabel(c TypeChange) string {
	switch {
	case c.New:
		return c.Type + " (new)"
	case c.Gone:
		return c.Type + " (removed)"
	}
	return c.Type
}

func displayActions(as []string) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = displayAction(a)
	}
	return out
}

// writeWrapped prints actions indented, comma-separated, in lines of up to 100 columns.
func writeWrapped(w io.Writer, as []string) {
	line := "   "
	for i, a := range as {
		if i < len(as)-1 {
			a += ","
		}
		if len(line) > 3 && len(line)+1+len(a) > 100 {
			fmt.Fprintln(w, line)
			line = "   "
		}
		line += " " + a
	}
	fmt.Fprintln(w, line)
}

// WriteText prints the diff for a terminal.
func (d Diff) WriteText(w io.Writer) {
	if d.Empty() {
		fmt.Fprintln(w, "execution policy: no change")
		return
	}
	fmt.Fprintf(w, "execution policy: %s\n", d.summary())
	for _, c := range d.Added {
		fmt.Fprintf(w, "\n+ %s\n", typeLabel(c))
		for _, r := range c.Risks {
			fmt.Fprintf(w, "    ! %s\n", riskText[r])
		}
		if len(c.Actions) > 0 {
			writeWrapped(w, displayActions(c.Actions))
		}
	}
	if len(d.NewRoles) > 0 {
		fmt.Fprintf(w, "\n+ iam:PassRole on\n    %s\n", strings.Join(d.NewRoles, "\n    "))
	}
	for _, c := range d.Removed {
		fmt.Fprintf(w, "\n- %s\n", typeLabel(c))
		if len(c.Actions) > 0 {
			writeWrapped(w, displayActions(c.Actions))
		}
	}
	if len(d.Dropped) > 0 {
		fmt.Fprintf(w, "\n%d action%s no longer needed; `apply` keeps them unless --prune\n", len(d.Dropped), plural(len(d.Dropped)))
	}
}

// WriteMarkdown prints the diff as a pull request comment.
func (d Diff) WriteMarkdown(w io.Writer) {
	if d.Empty() {
		fmt.Fprintln(w, "### Execution policy: no change")
		fmt.Fprintln(w, "\n<sub>cfn-exec-policy</sub>")
		return
	}
	fmt.Fprintf(w, "### Execution policy: %s\n", d.summary())
	fmt.Fprintln(w)
	if len(d.Risks) > 0 {
		for _, r := range d.Risks {
			var types []string
			for _, c := range d.Added {
				if slices.Contains(c.Risks, r) {
					types = append(types, "`"+c.Type+"`")
				}
			}
			fmt.Fprintf(w, "> [!WARNING]\n> %s: %s\n\n", strings.ToUpper(riskText[r][:1])+riskText[r][1:], strings.Join(types, ", "))
		}
	}
	fmt.Fprintln(w, "| | Resource type | Actions |\n|---|---|---|")
	row := func(kind string, c TypeChange) {
		as := displayActions(c.Actions)
		for i, a := range as {
			as[i] = "`" + a + "`"
		}
		cell := strings.Join(as, " ")
		if len(as) > 8 {
			cell = fmt.Sprintf("<details><summary>%d actions</summary>%s</details>", len(as), cell)
		}
		switch {
		case cell == "" && c.Gone:
			cell = "still needed by other types"
		case cell == "" && slices.Contains(c.Risks, riskUnknown):
			cell = "none: unknown type"
		case cell == "":
			cell = "none new"
		}
		typ := "`" + c.Type + "`"
		if c.Type == dynamicRefs {
			typ = c.Type
		}
		fmt.Fprintf(w, "| %s | %s | %s |\n", kind, typ, cell)
	}
	for _, c := range d.Added {
		kind := "changed"
		if c.New {
			kind = "added"
		}
		row(kind, c)
	}
	if len(d.NewRoles) > 0 {
		roles := make([]string, len(d.NewRoles))
		for i, r := range d.NewRoles {
			roles[i] = "`" + r + "`"
		}
		fmt.Fprintf(w, "| added | iam:PassRole on | %s |\n", strings.Join(roles, " "))
	}
	for _, c := range d.Removed {
		row("removed", c)
	}
	if len(d.Dropped) > 0 {
		fmt.Fprintf(w, "\n%d action%s no longer needed; `apply` keeps them unless `--prune`.\n", len(d.Dropped), plural(len(d.Dropped)))
	}
	fmt.Fprintln(w, "\n<sub>cfn-exec-policy</sub>")
}

// failed returns the risks of the diff that --fail-on names ("any" for any added action).
func (d Diff) failed(failOn []string) []string {
	var out []string
	for _, f := range failOn {
		switch {
		case f == "any" && (d.addedActions() > 0 || len(d.NewRoles) > 0):
			out = append(out, "any")
		case slices.Contains(d.Risks, f):
			out = append(out, f)
		}
	}
	return out
}
