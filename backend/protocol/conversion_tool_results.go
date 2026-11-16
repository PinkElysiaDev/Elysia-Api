package protocol

import (
	"fmt"
)

func validateToolResultErrors(nodes []Node, at string) error {
	for i, n := range nodes {
		path := fmt.Sprintf("%s/%d", at, i)
		if n.Kind == ToolResultNode {
			fields, _ := n.Status.ReadObject()
			if value := fields["isError"]; !value.IsZero() {
				var flag bool
				if value.IsNull() || value.Decode(&flag) != nil {
					return streamIssue(InvalidInput, path+"/status/isError", "tool error flag must be a boolean")
				}
			}
		}
		if err := validateToolResultErrors(n.Children, path+"/children"); err != nil {
			return err
		}
		if err := validateToolResultErrors(n.ReasoningContent, path+"/reasoningContent"); err != nil {
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
