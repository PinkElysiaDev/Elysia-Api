# AGENTS.md — Implemented Agent Notes

These Agent Notes describe shipped decisions. Follow the [root instructions](../../../CLAUDE.md) and [Agent Note format](../README.md#文件格式).

Every Agent Note is **bilingual as a zh/en file pair**: `yyyy-mm-dd-topic-title.zh.md` (Chinese) and `yyyy-mm-dd-topic-title.en.md` (English) must both exist and mirror each other. Keep both files in sync when updating facts.

## Keep an implemented Agent Note current with what actually shipped

Keep paths, symbols, defaults, mechanisms and measurements current in the same change that alters them. Rewrite stale facts in place; do not append change history.

### This is not a license to rewrite the _decision_

Update factual realization in place. A reversal of the decision or its rationale requires a new Agent Note and cross-link.
