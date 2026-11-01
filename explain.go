package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// explainShown is how many resources a text explanation lists per source.
const explainShown = 5

// actionName shows a passRole token as the action it stands for.
func actionName(a string) string {
	if svc, ok := strings.CutPrefix(a, passRole); ok {
		return "iam:PassRole (to " + svc + ")"
	}
	return a
}

// WriteExplain prints, per action, the sources and resources that need it.
func WriteExplain(w io.Writer, by map[string][]Why) {
	for _, a := range sortedKeys(by) {
		fmt.Fprintln(w, actionName(a))
		var sources []string
		bySource := map[string][]string{}
		for _, why := range by[a] {
			if bySource[why.Source] == nil {
				sources = append(sources, why.Source)
			}
			what := why.Template
			if why.LogicalID != "" {
				what = fmt.Sprintf("%s %s (%s)", why.Type, why.LogicalID, why.Template)
			}
			bySource[why.Source] = append(bySource[why.Source], what)
		}
		for _, src := range sources {
			l := bySource[src]
			more := ""
			if len(l) > explainShown {
				l, more = l[:explainShown], fmt.Sprintf(", and %d more", len(l)-explainShown)
			}
			fmt.Fprintf(w, "  %s: %s%s\n", src, strings.Join(l, ", "), more)
		}
	}
}

// WriteExplainJSON writes the same as a list of {action, needs}.
func WriteExplainJSON(w io.Writer, by map[string][]Why) error {
	type entry struct {
		Action string `json:"action"`
		Needs  []Why  `json:"needs"`
	}
	out := []entry{}
	for _, a := range sortedKeys(by) {
		out = append(out, entry{actionName(a), by[a]})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
