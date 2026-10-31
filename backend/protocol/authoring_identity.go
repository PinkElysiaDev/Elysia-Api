package protocol

// ProtocolIdentityFields selects the authored identity without compiling an
// incomplete draft. The returned path locates those fields in the document.
// Legacy identities remain readable while their execution path is retired.
func ProtocolIdentityFields(document Object) (Object, string, error) {
	var version int
	if document["schemaVersion"].Decode(&version) == nil && (version == 3 || version == ProtocolPackageSchemaVersion) {
		fields, err := document["spec"].ReadObject()
		return fields, "/spec", err
	}
	return document, "", nil
}
