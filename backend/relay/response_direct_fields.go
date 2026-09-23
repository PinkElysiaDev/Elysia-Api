package relay

// responseDirectFields 是响应映射的「直接路径」字段全集（声明式模板的
// 可映射键）。校验、编译、映射判定与 legacy 合并四处共用同一份表，
// 新增路径字段只改这里与结构体定义。
var responseDirectFields = []struct {
	name   string
	get    func(CustomProtocolResponse) string
	set    func(*CustomProtocolResponse, string)
	legacy string
}{
	{"idPath", func(r CustomProtocolResponse) string { return r.IDPath }, func(r *CustomProtocolResponse, v string) { r.IDPath = v }, "id"},
	{"modelPath", func(r CustomProtocolResponse) string { return r.ModelPath }, func(r *CustomProtocolResponse, v string) { r.ModelPath = v }, "model"},
	{"statusPath", func(r CustomProtocolResponse) string { return r.StatusPath }, func(r *CustomProtocolResponse, v string) { r.StatusPath = v }, "status"},
	{"textPath", func(r CustomProtocolResponse) string { return r.TextPath }, func(r *CustomProtocolResponse, v string) { r.TextPath = v }, "text"},
	{"reasoningPath", func(r CustomProtocolResponse) string { return r.ReasoningPath }, func(r *CustomProtocolResponse, v string) { r.ReasoningPath = v }, "reasoning"},
	{"toolCallsPath", func(r CustomProtocolResponse) string { return r.ToolCallsPath }, func(r *CustomProtocolResponse, v string) { r.ToolCallsPath = v }, "tool_calls"},
	{"usagePath", func(r CustomProtocolResponse) string { return r.UsagePath }, func(r *CustomProtocolResponse, v string) { r.UsagePath = v }, "usage"},
	{"finishReasonPath", func(r CustomProtocolResponse) string { return r.FinishReasonPath }, func(r *CustomProtocolResponse, v string) { r.FinishReasonPath = v }, "finish_reason"},
	{"errorPath", func(r CustomProtocolResponse) string { return r.ErrorPath }, func(r *CustomProtocolResponse, v string) { r.ErrorPath = v }, "error"},
	{"signaturePath", func(r CustomProtocolResponse) string { return r.SignaturePath }, func(r *CustomProtocolResponse, v string) { r.SignaturePath = v }, "signature"},
	{"signatureProviderPath", func(r CustomProtocolResponse) string { return r.SignatureProviderPath }, func(r *CustomProtocolResponse, v string) { r.SignatureProviderPath = v }, "signature_provider"},
	{"encryptedContentPath", func(r CustomProtocolResponse) string { return r.EncryptedContentPath }, func(r *CustomProtocolResponse, v string) { r.EncryptedContentPath = v }, "encrypted_content"},
	{"refusalPath", func(r CustomProtocolResponse) string { return r.RefusalPath }, func(r *CustomProtocolResponse, v string) { r.RefusalPath = v }, "refusal"},
	{"citationsPath", func(r CustomProtocolResponse) string { return r.CitationsPath }, func(r *CustomProtocolResponse, v string) { r.CitationsPath = v }, "citations"},
}
