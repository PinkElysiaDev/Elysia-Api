package protocol

import "fmt"

func (compiler *packageStreamCompiler) checkLifecycle(path string) error {
	if !compiler.actions[wireStart] || !compiler.actions[wireFinish] && !compiler.actions[wireFail] && !compiler.actions[wireCancel] {
		return fmt.Errorf("%s/events: requires a stream start and terminal action", path)
	}
	for index, entity := range compiler.contract.entities {
		actions := compiler.entityActions[index]
		if !actions[wireEntityStart] {
			return fmt.Errorf("%s/entities/%s: entity has no start action", path, entity.name)
		}
	}
	return nil
}
