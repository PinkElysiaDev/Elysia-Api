package protocol

import "slices"

// CheckTokenCountBinding checks an auxiliary operation against the pinned model
// or ingress binding. A generation-only operation selection does not select a
// counter; the caller must resolve an explicit count operation independently.
func CheckTokenCountBinding(binding Binding, compiled *Compiled, operation string) []ConversionIssue {
	if issues := checkBindingContract(binding, compiled); len(issues) != 0 {
		return issues
	}
	issue := bindingDiagnostic(compiled)
	if _, exists := compiled.TokenCountRequestProtocol(operation); !exists {
		issue.Code, issue.Path, issue.Reason = UnsupportedCapability, "/operations", "protocol has no declared token counting operation"
		return []ConversionIssue{issue}
	}
	if !slices.Contains(binding.Transports, HTTPJSON) {
		issue.Code, issue.Path, issue.Reason = UnsupportedCapability, "/binding/transports", "token counting requires an allowed HTTP JSON transport"
		return []ConversionIssue{issue}
	}
	return nil
}

// CheckTokenCountRequest checks actual counted input against the counter's
// request capabilities and the already selected model/group permissions.
func CheckTokenCountRequest(request *Request, compiled *Compiled, operation string, binding Binding, scope Scope) []ConversionIssue {
	count, err := compiled.tokenCounting(operation)
	if err != nil {
		return err.(*ConversionError).Issues
	}
	return CheckRequest(request, bindingTarget(count.request, binding, EncodeRequest, scope), count.request.limits)
}
