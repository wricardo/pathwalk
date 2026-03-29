// Package tools provides built-in tool implementations for pathwalk engines.
//
// This file provides the BuiltinTools() convenience function that returns
// all general-purpose tools (jq, grep, http_request) as a single bundle.
package tools

import pathwalk "github.com/wricardo/pathwalk"

// BuiltinTools returns all general-purpose built-in tools: jq, grep, and http_request.
// This is a convenience function for quickly adding common tools to an engine.
func BuiltinTools() []pathwalk.Tool {
	var tools []pathwalk.Tool
	tools = append(tools, JqTool{}.AsTools()...)
	tools = append(tools, GrepTool{}.AsTools()...)
	tools = append(tools, HTTPTool{}.AsTools()...)
	return tools
}

// BuiltinToolsWithHTTPHeaders returns all general-purpose built-in tools
// with custom default HTTP headers applied to the http_request tool.
func BuiltinToolsWithHTTPHeaders(defaultHeaders map[string]string) []pathwalk.Tool {
	var tools []pathwalk.Tool
	tools = append(tools, JqTool{}.AsTools()...)
	tools = append(tools, GrepTool{}.AsTools()...)
	tools = append(tools, HTTPTool{DefaultHeaders: defaultHeaders}.AsTools()...)
	return tools
}
