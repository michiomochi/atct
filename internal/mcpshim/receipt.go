package mcpshim

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// A state-changing call answers with a receipt: the ids, status and times the
// caller continues with, not the bodies it just sent. Bodies are read through
// the history and get tools.
type responseKind int

const (
	// kindPassthrough is the zero value, so a method that is not listed is
	// returned untouched.
	kindPassthrough responseKind = iota
	kindReceipt
	// kindDeliver is a receipt that keeps one report, the one the caller
	// called the method to read.
	kindDeliver
)

var receiptMethods = []string{
	"project.claim", "project.release",
	"goal.claim", "goal.release", "goal.withdraw", "goal.update_request_report",
	"goal.set_derived_from", "goal.complete", "goal.review.request", "goal.review.complete",
	"task.create", "task.create_handoff.receive", "task.update", "task.update_content",
	"task.handoff.request", "task.handoff.receive", "task.handoff.review.request",
	"task.handoff.review.reject", "task.handoff.complete", "task.handoff.report.amend",
	"goal.handoff.request", "goal.handoff.receive", "goal.handoff.review.request",
	"goal.handoff.review.reject", "goal.handoff.complete", "goal.handoff.report.amend",
	"plan.handoff.review.request", "plan.handoff.review.reject", "plan.handoff.complete",
	"handoff.entry.append", "goal.handoff.entry.append",
	"decision.withdraw", "handoff.recover",
}

// deliverMethods maps a method to the one key (compared by normalizeKey) kept
// at the top level of its data.
var deliverMethods = map[string]string{
	"task.handoff.review.receive":        "reviewrequestreport",
	"goal.handoff.review.receive":        "reviewrequestreport",
	"plan.handoff.review.receive":        "reviewrequestreport",
	"task.handoff.review.reject.receive": "reviewrejectreport",
	"goal.handoff.review.reject.receive": "reviewrejectreport",
	"plan.handoff.review.reject.receive": "reviewrejectreport",
}

var receiptKinds = func() map[string]responseKind {
	kinds := map[string]responseKind{}
	for _, m := range receiptMethods {
		kinds[m] = kindReceipt
	}
	for m := range deliverMethods {
		kinds[m] = kindDeliver
	}
	return kinds
}()

var blockedKeys = func() map[string]bool {
	blocked := map[string]bool{}
	for _, k := range []string{
		"content", "spec", "plan", "work_done", "now_possible", "how_to_verify", "surprises",
		"needs_review", "result_summary", "request_report", "review_request_report",
		"review_reject_report", "reject_report", "complete_report", "recovery_report",
		"entries", "history", "has_more", "next_cursor", "description", "body", "question", "options",
	} {
		blocked[normalizeKey(k)] = true
	}
	return blocked
}()

// normalizeKey makes RequestReport and request_report the same key.
func normalizeKey(key string) string {
	return strings.ToLower(strings.ReplaceAll(key, "_", ""))
}

// shapeData drops the blocked keys from a receipt or deliver response. Numbers
// stay json.Number so an id beyond 2^53 keeps every digit. If data cannot be
// decoded it is returned as it came.
func shapeData(method string, data json.RawMessage) json.RawMessage {
	kind := receiptKinds[method]
	if kind == kindPassthrough {
		return data
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return data
	}
	keep := ""
	if kind == kindDeliver {
		keep = deliverMethods[method]
	}
	if object, ok := value.(map[string]any); ok && keep != "" {
		kept := map[string]any{}
		for k, v := range object {
			if normalizeKey(k) == keep {
				kept[k] = v
				delete(object, k)
			}
		}
		value = dropBlocked(object)
		for k, v := range kept {
			value.(map[string]any)[k] = v
		}
	} else {
		value = dropBlocked(value)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return data
	}
	return json.RawMessage(bytes.TrimRight(out.Bytes(), "\n"))
}

func dropBlocked(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if blockedKeys[normalizeKey(k)] {
				delete(v, k)
				continue
			}
			v[k] = dropBlocked(child)
		}
	case []any:
		for i, child := range v {
			v[i] = dropBlocked(child)
		}
	}
	return value
}

// unappliedState is what this session was last told. The shim is one process
// per session, so the Client holds it.
type unappliedState struct {
	mu   sync.Mutex
	seen bool
	sig  string
}

func unappliedSignature(list []UnappliedDecisionNotice) string {
	var b strings.Builder
	for _, n := range list {
		b.WriteString(strconv.FormatInt(n.DecisionID, 10))
		b.WriteByte(0)
		b.WriteString(n.Question)
		b.WriteByte(1)
	}
	return b.String()
}

// reconcileUnapplied decides what the response says about unapplied decisions:
// the list (a pointer so an empty list still prints as []), or only a count
// when nothing changed since the last response that carried the question.
func (c *Client) reconcileUnapplied(method string, params any, list []UnappliedDecisionNotice) (*[]UnappliedDecisionNotice, int) {
	if p, ok := params.(map[string]any); !ok || p["include_unapplied_answers"] != true {
		if len(list) == 0 {
			return nil, 0
		}
		return &list, 0
	}
	sig := unappliedSignature(list)
	c.unapplied.mu.Lock()
	first := !c.unapplied.seen
	changed := first || sig != c.unapplied.sig
	c.unapplied.seen, c.unapplied.sig = true, sig
	c.unapplied.mu.Unlock()

	switch {
	case len(list) > 0 && (changed || method == "goal.list"):
		return &list, 0
	case len(list) == 0 && changed && !first:
		return &[]UnappliedDecisionNotice{}, 0 // changed to none: say [] so the caller drops the old list
	case len(list) > 0:
		return nil, len(list)
	}
	return nil, 0
}
