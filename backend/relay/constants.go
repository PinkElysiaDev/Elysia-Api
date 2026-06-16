package relay

const (
	SSEBufInitial          = 64 * 1024
	SSEBufMax              = 16 * 1024 * 1024
	ClaudeDefaultMaxTokens = 65536
	EffortBudgetLowCeil    = 1000
	EffortBudgetHighFloor  = 20000
	EffortBudgetDefault    = 10000
)
