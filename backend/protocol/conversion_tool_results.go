package protocol

func validateToolResultErrors(nodes []Node, at string) error {
	var buffer [256]byte
	return validateToolResultErrorsPath(nodes, append(diagnosticPath(buffer[:0]), at...))
}

func validateToolResultErrorsPath(nodes []Node, at diagnosticPath) error {
	for i, n := range nodes {
		path := at.index(i)
		if n.Kind == ToolResultNode {
			fields, _ := n.Status.ReadObject()
			if value := fields["isError"]; !value.IsZero() {
				var flag bool
				if value.IsNull() || value.Decode(&flag) != nil {
					return streamIssue(InvalidInput, path.suffix("/status/isError").String(), "tool error flag must be a boolean")
				}
			}
		}
		if err := validateToolResultErrorsPath(n.Children, path.suffix("/children")); err != nil {
			return err
		}
		if err := validateToolResultErrorsPath(n.ReasoningContent, path.suffix("/reasoningContent")); err != nil {
			return err
		}
	}
	return nil
}

func toolResultError(status Value, path string) (bool, error) {
	if status.IsZero() {
		return false, nil
	}
	fields, err := status.ReadObject()
	if err != nil || len(fields) != 1 || fields["isError"].IsZero() {
		return false, streamIssue(UnsupportedCapability, path, "tool result status requires one boolean error flag")
	}
	var failed bool
	if fields["isError"].IsNull() || fields["isError"].Decode(&failed) != nil {
		return false, streamIssue(InvalidInput, path+"/isError", "tool error flag must be a boolean")
	}
	return failed, nil
}
