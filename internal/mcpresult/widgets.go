package mcpresult

import (
	"github.com/Serialeo/agentdock-protocol/mcpapps"
	"strings"
)

// 界面在浏览器内推导显示字段，不让 action/count/正文副本重新进入模型结果。
// 上游 UI 固定在 go.mod 的协议版本；回归验证插入点和脚本行为，升级依赖时显式适配。
func WidgetHTML(view, title string) string {
	return AdaptWidgetHTML(mcpapps.HTML(view, title))
}

// 兼容同一 UI contract 的旧节点组件，只在已知模板插入点适配，且重复代理不重复注入。
func AdaptWidgetHTML(html string) string {
	if strings.Contains(html, "function compactViewData(") {
		return html
	}
	if !strings.Contains(html, "  function render(data){") || !strings.Contains(html, "if(isObject(data))render(data);") {
		return html
	}
	html = strings.Replace(html, "  function render(data){", compactViewAdapter+"\n  function render(data){", 1)
	return strings.Replace(html, "if(isObject(data))render(data);", "if(isObject(data))render(compactViewData(data,message.params));", 1)
}

const compactViewAdapter = `
  function compactViewData(data,result){
    let value={...data};
    if(expectedView==="dynamic_mcp"&&isObject(value.result)&&result&&Array.isArray(result.content)){
      value.result={...value.result,content:result.content};
    }
    if(expectedView==="task_progress"){
      value.action=value.action||(Array.isArray(value.tasks)?"list":"task");
      for(const key of ["task","task_summary"]){
        if(!isObject(value[key]))continue;
        const task={...value[key]};
        if(Array.isArray(task.steps)){
          task.step_count=task.steps.length;
          task.completed_step_count=task.steps.filter(step=>isObject(step)&&step.status==="completed").length;
        }
        value[key]=task;
      }
    }
    if(expectedView==="workflow"&&Array.isArray(value.candidates))value.action=value.action||"match";
    if(expectedView==="acp_status"){
      value.action=value.action||(Array.isArray(value.sessions)?"list":"status");
      if(value.auth_method_id&&value.authenticated===undefined)value.authenticated=true;
    }
    return value;
  }
`
