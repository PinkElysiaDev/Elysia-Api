package relay

import "context"

type streamBatchDecoder interface {
	Decode(SSEEvent) ([]MaheshvaraStreamEvent, bool, error)
	TerminalReceived() bool
}

// ForEachBatch drains native events with the same policy as custom definitions.
func (decoder *MaheshvaraStreamDecoder) ForEachBatch(ctx context.Context, reader *SSEEventReader, handle func(SSEEvent, []MaheshvaraStreamEvent, bool) error) error {
	return readStreamBatches(ctx, reader, &CustomProtocolStreamDecoder{native: decoder}, handle)
}

// readStreamBatches uses one terminal-drain policy for native adapters,
// declarative mappings, previews and Agent calls. Invalid late frames remain
// contract errors; a read timeout after a real terminal only ends the drain.
func readStreamBatches(ctx context.Context, reader *SSEEventReader, decoder streamBatchDecoder, handle func(SSEEvent, []MaheshvaraStreamEvent, bool) error) error {
	for {
		wireEvent, hasMore, err := reader.Read(ctx, PostTerminalDrainIdle(decoder.TerminalReceived()))
		if err != nil {
			if BenignPostTerminalErr(decoder.TerminalReceived(), err) {
				return nil
			}
			return err
		}
		if !hasMore {
			return nil
		}
		wasTerminal := decoder.TerminalReceived()
		events, isDone, err := decoder.Decode(wireEvent)
		if err != nil {
			return err
		}
		if err := handle(wireEvent, events, wasTerminal); err != nil {
			return err
		}
		if isDone {
			return nil
		}
	}
}
