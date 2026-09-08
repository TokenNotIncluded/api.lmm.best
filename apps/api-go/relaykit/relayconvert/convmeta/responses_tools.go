package convmeta

// ResponsesToolIdentity records the Responses identity behind an upstream
// function name. ToolSearch distinguishes client-executed discovery calls from
// ordinary functions, including an ordinary function named "tool_search".
type ResponsesToolIdentity struct {
	Name       string
	Namespace  string
	ToolSearch bool
}

// ResponsesToolMap is request-scoped: keys are flat upstream function names.
type ResponsesToolMap map[string]ResponsesToolIdentity
