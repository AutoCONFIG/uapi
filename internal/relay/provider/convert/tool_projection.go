package convert

import (
	"encoding/json"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/schema"
)

func functionToolProjections(tools []schema.Tool) []schema.Tool {
	out := make([]schema.Tool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, functionToolProjectionsFromTool(tool)...)
	}
	return out
}

func functionToolProjectionsFromTool(tool schema.Tool) []schema.Tool {
	switch strings.TrimSpace(tool.Type) {
	case "namespace":
		return functionToolProjectionsFromNamespace(tool)
	case "mcp", "file_search", "code_interpreter", "computer_use_preview", "image_generation", "local_shell", "custom", "tool_search":
		return nil
	}
	name, description, parameters := normalizedFunctionTool(tool)
	if name == "" {
		return nil
	}
	projected := projectedFunctionTool(name, description, parameters)
	projected.Extra = allowedProjectedToolExtra(tool.Extra)
	projected.Function.Extra = allowedProjectedFunctionExtra(tool.Extra)
	if tool.Function != nil {
		projected.Function.Extra = mergeRawMaps(projected.Function.Extra, allowedProjectedFunctionExtra(tool.Function.Extra))
	}
	return []schema.Tool{projected}
}

func functionToolProjectionsFromNamespace(tool schema.Tool) []schema.Tool {
	childrenRaw := tool.Extra["tools"]
	if len(childrenRaw) == 0 || string(childrenRaw) == "null" {
		return nil
	}
	var children []schema.Tool
	if err := json.Unmarshal(childrenRaw, &children); err != nil {
		return nil
	}
	out := make([]schema.Tool, 0, len(children))
	namespace := strings.TrimSpace(tool.Name)
	for _, child := range children {
		for _, projected := range functionToolProjectionsFromTool(child) {
			name, description, parameters := normalizedFunctionTool(projected)
			if name == "" {
				continue
			}
			out = append(out, projectedFunctionTool(qualifyResponsesNamespaceToolName(namespace, name), description, parameters))
		}
	}
	return out
}

func projectedFunctionTool(name, description string, parameters json.RawMessage) schema.Tool {
	parameters = normalizeToolSchemaRaw(parameters)
	return schema.Tool{
		Type:        "function",
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Parameters:  parameters,
		Function: &schema.ToolFunction{
			Name:        strings.TrimSpace(name),
			Description: strings.TrimSpace(description),
			Parameters:  parameters,
		},
	}
}

func allowedProjectedToolExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	if raw := extra["cache_control"]; len(raw) > 0 {
		out["cache_control"] = append(json.RawMessage(nil), raw...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func allowedProjectedFunctionExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	if raw := extra["strict"]; len(raw) > 0 {
		out["strict"] = append(json.RawMessage(nil), raw...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeRawMaps(dst, src map[string]json.RawMessage) map[string]json.RawMessage {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]json.RawMessage{}
	}
	for key, raw := range src {
		dst[key] = append(json.RawMessage(nil), raw...)
	}
	return dst
}

func qualifyResponsesNamespaceToolName(namespaceName, childName string) string {
	namespaceName = strings.TrimSpace(namespaceName)
	childName = strings.TrimSpace(childName)
	if childName == "" || namespaceName == "" || strings.HasPrefix(childName, "mcp__") {
		return childName
	}
	if strings.HasPrefix(childName, namespaceName) {
		return childName
	}
	if strings.HasSuffix(namespaceName, "__") {
		return namespaceName + childName
	}
	return namespaceName + "__" + childName
}

func projectFunctionToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}
	var choice map[string]interface{}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return raw
	}
	choiceType, _ := choice["type"].(string)
	if choiceType != "function" {
		return raw
	}
	namespace := toolChoiceNamespace(choice)
	if namespace == "" {
		return raw
	}
	if function, ok := choice["function"].(map[string]interface{}); ok {
		if name, ok := function["name"].(string); ok {
			function["name"] = qualifyResponsesNamespaceToolName(namespace, name)
			delete(function, "namespace")
			delete(choice, "namespace")
			if out, err := json.Marshal(choice); err == nil {
				return out
			}
		}
	}
	if name, ok := choice["name"].(string); ok {
		choice["name"] = qualifyResponsesNamespaceToolName(namespace, name)
		delete(choice, "namespace")
		if out, err := json.Marshal(choice); err == nil {
			return out
		}
	}
	return raw
}

func toolChoiceNamespace(choice map[string]interface{}) string {
	if namespace, ok := choice["namespace"].(string); ok {
		return strings.TrimSpace(namespace)
	}
	if function, ok := choice["function"].(map[string]interface{}); ok {
		if namespace, ok := function["namespace"].(string); ok {
			return strings.TrimSpace(namespace)
		}
	}
	return ""
}
