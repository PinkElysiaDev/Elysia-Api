package protocol

import "fmt"

func compileMetadataControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	if path == "/value" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			if output.IsZero() {
				return fmt.Errorf("metadata values must remain present; omit the entry explicitly")
			}
			return assignCoreValue(output, &document.(*ResponseMetadata).Value, reader)
		}, AnyType, nil
	}
	var selectField func(*ResponseMetadata) *string
	switch path {
	case "/codec":
		selectField = func(value *ResponseMetadata) *string { return &value.Codec }
	case "/location":
		selectField = func(value *ResponseMetadata) *string { return &value.Location }
	case "/name":
		selectField = func(value *ResponseMetadata) *string { return &value.Name }
	case "/update":
		selectField = func(value *ResponseMetadata) *string { return &value.Update }
	default:
		return nil, "", fmt.Errorf("metadata controls cannot write %s", path)
	}
	return func(document any, output JSONOutput, _ *valueReader) error {
		value := document.(*ResponseMetadata)
		if path == "/update" && output.IsZero() {
			value.Update = ""
			return nil
		}
		text, err := readPlannedString(output)
		if err == nil {
			*selectField(value) = text
		}
		return err
	}, StringType, nil
}

func compileUsageControlSetter(path string) (func(any, JSONOutput, *valueReader) error, JSONType, error) {
	pointer, err := parsePointer(path)
	if err != nil {
		return nil, "", err
	}
	if len(pointer) == 2 && pointer[0] == "details" {
		name := pointer[1]
		return func(document any, output JSONOutput, reader *valueReader) error {
			usage := document.(*Usage)
			if output.IsZero() {
				delete(usage.Details, name)
				return nil
			}
			var counter Counter
			if err := writeCoreCounter(output, &counter, reader); err != nil {
				return err
			}
			if usage.Details == nil {
				usage.Details = make(map[string]Counter)
			}
			usage.Details[name] = counter
			return nil
		}, ObjectType, nil
	}
	if path == "/attribution" || path == "/cacheBilling" {
		return func(document any, output JSONOutput, reader *valueReader) error {
			usage := document.(*Usage)
			value := &usage.Attribution
			if path == "/cacheBilling" {
				value = &usage.CacheBilling
			}
			if output.IsZero() {
				*value = Value{}
				return nil
			}
			return assignCoreValue(output, value, reader)
		}, AnyType, nil
	}
	var selectField func(*Usage) **Counter
	switch path {
	case "/input":
		selectField = func(value *Usage) **Counter { return &value.Input }
	case "/output":
		selectField = func(value *Usage) **Counter { return &value.Output }
	case "/total":
		selectField = func(value *Usage) **Counter { return &value.Total }
	case "/cacheRead":
		selectField = func(value *Usage) **Counter { return &value.CacheRead }
	case "/cacheCreation":
		selectField = func(value *Usage) **Counter { return &value.CacheCreation }
	default:
		return nil, "", fmt.Errorf("usage controls cannot write %s", path)
	}
	return func(document any, output JSONOutput, reader *valueReader) error {
		var counter *Counter
		if !output.IsZero() {
			if err := writeCoreCounterPointer(output, &counter, reader); err != nil {
				return err
			}
		}
		*selectField(document.(*Usage)) = counter
		return nil
	}, ObjectType, nil
}

func replaceControlUsage(output JSONOutput, target **Usage, reader *valueReader) error {
	var usage *Usage
	if !output.IsZero() {
		if err := writeCoreUsagePointer(output, &usage, reader); err != nil {
			return err
		}
	}
	*target = usage
	return nil
}
