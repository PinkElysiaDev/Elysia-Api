package relay

// responseDirectFields 是响应映射的「直接路径」字段全集（声明式模板的
// 可映射键）。校验、编译、映射判定与 legacy 合并四处共用同一份表，
// 新增路径字段只改这里与结构体定义。
var responseDirectFields = []struct {
	name   string
	get    func(CustomProtocolResponse) string
	legacy string
}{
	{"idPath", func(r CustomProtocolResponse) string { return r.IDPath }, "id"},
	{"modelPath", func(r CustomProtocolResponse) string { return r.ModelPath }, "model"},
	{"statusPath", func(r CustomProtocolResponse) string { return r.StatusPath }, "status"},
	{"textPath", func(r CustomProtocolResponse) string { return r.TextPath }, "text"},
	{"reasoningPath", func(r CustomProtocolResponse) string { return r.ReasoningPath }, "reasoning"},
	{"toolCallsPath", func(r CustomProtocolResponse) string { return r.ToolCallsPath }, "tool_calls"},
	{"usagePath", func(r CustomProtocolResponse) string { return r.UsagePath }, "usage"},
	{"finishReasonPath", func(r CustomProtocolResponse) string { return r.FinishReasonPath }, "finish_reason"},
	{"errorPath", func(r CustomProtocolResponse) string { return r.ErrorPath }, "error"},
	{"signaturePath", func(r CustomProtocolResponse) string { return r.SignaturePath }, "signature"},
	{"signatureProviderPath", func(r CustomProtocolResponse) string { return r.SignatureProviderPath }, "signature_provider"},
	{"encryptedContentPath", func(r CustomProtocolResponse) string { return r.EncryptedContentPath }, "encrypted_content"},
	{"refusalPath", func(r CustomProtocolResponse) string { return r.RefusalPath }, "refusal"},
	{"citationsPath", func(r CustomProtocolResponse) string { return r.CitationsPath }, "citations"},
}
