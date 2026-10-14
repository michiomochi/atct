package mcpshim

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func goalGetBody(long string) string {
	return `{"goal":{"id":382,"content":"C","spec":"` + long + `","plan":"` + long + `","status":"active","work_done":"` + long + `"},
 "tasks":[{"id":1,"goal_id":382,"title":"t1","description":"` + long + `","status":"done","agent":"a","order":1,"declare_key":"k"},
          {"id":2,"goal_id":382,"title":"t2","description":"` + long + `","status":"todo","agent":"a","order":2,"declare_key":"k"}],
 "goal_review":{"decision_id":7,"status":"open"}}`
}

func goalGetData(t *testing.T, fields []string, long string) (map[string]json.RawMessage, int) {
	t.Helper()
	c := startReceiptDaemon(t, func(string) string { return receiptEnvelope(goalGetBody(long)) })
	_, out, err := goalGet(context.Background(), c, GoalGetIn{GoalID: "382", Fields: fields})
	if err != nil {
		t.Fatalf("goalGet %v: %v", fields, err)
	}
	raw := out.Data.(json.RawMessage)
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("data %s: %v", raw, err)
	}
	return data, len(raw)
}

func keysOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("object %s: %v", raw, err)
	}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestGoalGetFieldsOmittedKeepsEverything(t *testing.T) {
	data, _ := goalGetData(t, nil, "x")
	for _, k := range []string{"goal", "tasks", "goal_review"} {
		if _, ok := data[k]; !ok {
			t.Errorf("omitted fields lost %s", k)
		}
	}
	if got := len(keysOf(t, data["goal"])); got != 6 {
		t.Errorf("goal keys = %d, want 6", got)
	}
}

func TestGoalGetFieldsContentOnly(t *testing.T) {
	data, _ := goalGetData(t, []string{"content"}, "x")
	if _, ok := data["tasks"]; ok {
		t.Error("tasks present without being asked")
	}
	if _, ok := data["goal_review"]; ok {
		t.Error("goal_review present without being asked")
	}
	if got := keysOf(t, data["goal"]); len(got) != 1 || got[0] != "content" {
		t.Errorf("goal keys = %v, want [content]", got)
	}
}

func TestGoalGetFieldsTaskSummary(t *testing.T) {
	data, _ := goalGetData(t, []string{"task_summary"}, "x")
	if _, ok := data["goal"]; ok && len(keysOf(t, data["goal"])) != 0 {
		t.Errorf("goal = %s, want empty", data["goal"])
	}
	var tasks []json.RawMessage
	if err := json.Unmarshal(data["task_summary"], &tasks); err != nil || len(tasks) != 2 {
		t.Fatalf("task_summary = %s (%v)", data["task_summary"], err)
	}
	if _, ok := data["tasks"]; ok {
		t.Error("tasks present when only task_summary asked")
	}
	got := keysOf(t, tasks[0])
	if len(got) != 4 {
		t.Errorf("task keys = %v, want id,title,status,order", got)
	}
	for _, k := range got {
		if k != "id" && k != "title" && k != "status" && k != "order" {
			t.Errorf("unexpected task key %s", k)
		}
	}
}

func TestGoalGetFieldsTasksAndReview(t *testing.T) {
	data, _ := goalGetData(t, []string{"tasks", "goal_review"}, "x")
	if _, ok := data["tasks"]; !ok {
		t.Error("tasks missing")
	}
	if _, ok := data["goal_review"]; !ok {
		t.Error("goal_review missing")
	}
}

func TestGoalGetFieldsRejectsUnknownName(t *testing.T) {
	c := startReceiptDaemon(t, func(string) string { return receiptEnvelope(goalGetBody("x")) })
	_, _, err := goalGet(context.Background(), c, GoalGetIn{GoalID: "382", Fields: []string{"nope"}})
	if err == nil {
		t.Fatal("unknown field accepted")
	}
	for _, want := range []string{"nope", "content", "spec", "tasks", "task_summary", "goal_review"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestGoalGetFieldsShrinksLongGoal(t *testing.T) {
	long := strings.Repeat("long text ", 500)
	_, full := goalGetData(t, nil, long)
	_, slim := goalGetData(t, []string{"content", "task_summary"}, long)
	if slim >= full {
		t.Errorf("fields response %d bytes, full %d bytes: not smaller", slim, full)
	}
}
