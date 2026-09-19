package mcpresult

import (
	"os/exec"
	"strings"
	"testing"
)

func TestWidgetsReconstructPresentationWithoutWireDuplicates(t *testing.T) {
	for _, view := range []string{"task_progress", "acp_status", "workflow", "dynamic_mcp"} {
		html := WidgetHTML(view, "test")
		if strings.Count(html, "function compactViewData(") != 1 || !strings.Contains(html, "render(compactViewData(data,message.params))") {
			t.Fatalf("widget adapter not installed for %s", view)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for JavaScript execution")
	}
	tests := []struct{ view, check string }{
		{"task_progress", `const original={task_summary:{steps:[{status:"completed"},{status:"pending"}]}};const data=compactViewData(original,{});if(data.action!=="task"||data.task_summary.step_count!==2||data.task_summary.completed_step_count!==1||original.action!==undefined)throw Error("task view");`},
		{"workflow", `if(compactViewData({candidates:[]},{}).action!=="match")throw Error("workflow view");`},
		{"acp_status", `if(compactViewData({sessions:[]},{}).action!=="list")throw Error("ACP view");`},
		{"dynamic_mcp", `const text={type:"text",text:"upstream"};const data=compactViewData({result:{}},{content:[text]});if(data.result.content[0].text!=="upstream")throw Error("dynamic content");`},
	}
	for _, test := range tests {
		t.Run(test.view, func(t *testing.T) {
			script := `const expectedView="` + test.view + `";function isObject(x){return !!x&&typeof x==="object"&&!Array.isArray(x);}` + compactViewAdapter + test.check
			if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
}

func TestWidgetAdapterIsIdempotentAndDoesNotChangeUnknownMarkup(t *testing.T) {
	html := WidgetHTML("task_progress", "task")
	if AdaptWidgetHTML(html) != html {
		t.Fatal("nested relay injected duplicate widget code")
	}
	unknown := "<html><body>vendor-owned</body></html>"
	if AdaptWidgetHTML(unknown) != unknown {
		t.Fatal("unknown markup changed")
	}
	description := Description("task_manage", "Task lifecycle")
	if Description("task_manage", description) != description {
		t.Fatal("relay duplicated checkpoint orientation")
	}
}
