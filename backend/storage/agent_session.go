package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elysia-api/backend/agent"
)

// Agent 会话持久化：agent_sessions / agent_messages 两张表。本文件实现
// agent.Store 接口（引擎读写路径）并提供管理面 CRUD（列表/创建/改设置/删除）。
// 测试凭证 test_api_key 经 secretCodec 透明加密；引擎读出时解密，管理面
// 永不回传明文（Settings.TestAPIKeySet 布尔标记替代）。

// AgentSessionUpsert 是创建会话的输入。
type AgentSessionUpsert struct {
	Title       string
	Mode        string // create | edit
	ProtocolID  string
	SeedConfig  string // 编辑模式的初始配置（同时作为草稿初值）
	TestBaseURL string
	Settings    agent.Settings
}

// CreateAgentSession 创建会话并返回完整聚合。
func (s *Store) CreateAgentSession(ctx context.Context, input AgentSessionUpsert) (*agent.Session, error) {
	id, err := newAgentSessionID()
	if err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = agent.ModeCreate
	}
	if mode != agent.ModeCreate && mode != agent.ModeEdit {
		return nil, fmt.Errorf("invalid agent session mode %q", mode)
	}
	now := nowString()
	encryptedKey := "" // 创建时不带凭证；API key 经 UpdateAgentSessionSettings/PATCH 写入
	draft := strings.TrimSpace(input.SeedConfig)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO agent_sessions
		(id, title, mode, protocol_id, seed_config, draft_config, test_base_url, test_api_key,
		 model_source_id, model_name, thinking_enabled, thinking_effort, plan_mode, allow_live_test, allow_save,
		 status, pending_action, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		id, strings.TrimSpace(input.Title), mode, strings.TrimSpace(input.ProtocolID),
		strings.TrimSpace(input.SeedConfig), draft, strings.TrimSpace(input.TestBaseURL), encryptedKey,
		input.Settings.ModelSourceID, input.Settings.ModelName,
		sqlBoolToInt(input.Settings.ThinkingEnabled), strings.TrimSpace(input.Settings.ThinkingEffort),
		sqlBoolToInt(input.Settings.PlanMode),
		agent.NormalizedPermission(input.Settings.AllowLiveTest), agent.NormalizedPermission(input.Settings.AllowSave),
		agent.StatusIdle, now, now); err != nil {
		return nil, err
	}
	return s.GetAgentSession(ctx, id)
}

// ListAgentSessions 按更新时间倒序返回会话摘要（不含消息、不含凭证）。
func (s *Store) ListAgentSessions(ctx context.Context) ([]agent.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, mode, protocol_id, seed_config, draft_config, draft_restore, plan_json,
		test_base_url, model_source_id, model_name, thinking_enabled, thinking_effort, plan_mode, allow_live_test, allow_save,
		status, pending_action, created_at, updated_at
		FROM agent_sessions ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []agent.Session{}
	for rows.Next() {
		var session agent.Session
		var mode, createdAt, updatedAt string
		var seed, draft, restore, plan, testBaseURL string
		var pending sql.NullString
		var thinkingEnabled, planMode int
		if err := rows.Scan(&session.ID, &session.Title, &mode, &session.ProtocolID, &seed, &draft, &restore, &plan,
			&testBaseURL, &session.Settings.ModelSourceID, &session.Settings.ModelName,
			&thinkingEnabled, &session.Settings.ThinkingEffort, &planMode, &session.Settings.AllowLiveTest, &session.Settings.AllowSave,
			&session.Status, &pending, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item, err := s.assembleAgentSession(session, mode, seed, draft, restore, plan, testBaseURL, "", pending.String, thinkingEnabled != 0, planMode != 0, createdAt, updatedAt, false)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// GetAgentSession 返回单个会话（管理面/详情用；测试凭证已解密）。
func (s *Store) GetAgentSession(ctx context.Context, id string) (*agent.Session, error) {
	return s.getAgentSession(ctx, id, true)
}

// GetSession 实现 agent.Store（引擎读路径）。
func (s *Store) GetSession(ctx context.Context, id string) (*agent.Session, error) {
	return s.getAgentSession(ctx, id, true)
}

func (s *Store) getAgentSession(ctx context.Context, id string, withSecret bool) (*agent.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, mode, protocol_id, seed_config, draft_config, draft_restore, plan_json,
		test_base_url, test_api_key, model_source_id, model_name, thinking_enabled, thinking_effort, plan_mode, allow_live_test, allow_save,
		status, pending_action, created_at, updated_at
		FROM agent_sessions WHERE id = ?`, strings.TrimSpace(id))
	session, err := s.scanAgentSessionRow(row, withSecret)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// scanner 兼容 sql.Row 与 sql.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanAgentSessionRow(row rowScanner, withSecret bool) (*agent.Session, error) {
	var session agent.Session
	var mode, createdAt, updatedAt string
	var seed, draft, restore, plan, testBaseURL, testAPIKey string
	var pending sql.NullString
	var thinkingEnabled, planMode int
	if err := row.Scan(&session.ID, &session.Title, &mode, &session.ProtocolID, &seed, &draft, &restore, &plan,
		&testBaseURL, &testAPIKey, &session.Settings.ModelSourceID, &session.Settings.ModelName,
		&thinkingEnabled, &session.Settings.ThinkingEffort, &planMode, &session.Settings.AllowLiveTest, &session.Settings.AllowSave,
		&session.Status, &pending, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return s.assembleAgentSession(session, mode, seed, draft, restore, plan, testBaseURL, testAPIKey, pending.String, thinkingEnabled != 0, planMode != 0, createdAt, updatedAt, withSecret)
}

func (s *Store) assembleAgentSession(session agent.Session, mode, seed, draft, restore, plan, testBaseURL, testAPIKey, pending string,
	thinkingEnabled, planMode bool, createdAt, updatedAt string, withSecret bool) (*agent.Session, error) {
	session.Mode = mode
	if seed != "" {
		session.SeedConfig = json.RawMessage(seed)
	}
	if draft != "" {
		session.DraftConfig = json.RawMessage(draft)
	}
	if restore != "" {
		session.DraftRestore = json.RawMessage(restore)
	}
	if plan != "" {
		var steps []agent.PlanStep
		if err := json.Unmarshal([]byte(plan), &steps); err == nil && len(steps) > 0 {
			session.Plan = steps
		}
	}
	session.TestBaseURL = testBaseURL
	session.Settings.ThinkingEnabled = thinkingEnabled
	session.Settings.PlanMode = planMode
	session.Settings.AllowLiveTest = agent.NormalizedPermission(session.Settings.AllowLiveTest)
	session.Settings.AllowSave = agent.NormalizedPermission(session.Settings.AllowSave)
	if withSecret && testAPIKey != "" {
		plaintext := s.decryptAgentKey(session.ID, testAPIKey)
		session.TestAPIKey = plaintext
		session.Settings.TestAPIKeySet = plaintext != ""
	} else {
		session.Settings.TestAPIKeySet = testAPIKey != ""
	}
	if pending != "" {
		var action agent.PendingAction
		if err := json.Unmarshal([]byte(pending), &action); err == nil && len(action.Calls) > 0 {
			session.PendingAction = &action
		}
	}
	session.CreatedAt = parseTime(createdAt)
	session.UpdatedAt = parseTime(updatedAt)
	return &session, nil
}

func (s *Store) decryptAgentKey(id, stored string) string {
	plaintext, err := s.codec.decrypt(stored)
	if err != nil {
		return ""
	}
	return plaintext
}

// UpdateAgentSessionSettings 管理面设置更新（PATCH）。SettingsPatch 全指针
// 字段：nil 不改、非 nil 覆盖——前端可安全发增量。
func (s *Store) UpdateAgentSessionSettings(ctx context.Context, id string, title *string,
	patch *agent.SettingsPatch, apiKey *string, clearAPIKey bool) (*agent.Session, error) {
	id = strings.TrimSpace(id)
	if _, err := s.getAgentSession(ctx, id, false); err != nil {
		return nil, err
	}
	sets := []string{"updated_at = ?"}
	args := []any{nowString()}
	if title != nil {
		sets = append(sets, "title = ?")
		args = append(args, strings.TrimSpace(*title))
	}
	if patch != nil {
		if patch.ModelSourceID != nil {
			sets = append(sets, "model_source_id = ?")
			args = append(args, strings.TrimSpace(*patch.ModelSourceID))
		}
		if patch.ModelName != nil {
			sets = append(sets, "model_name = ?")
			args = append(args, strings.TrimSpace(*patch.ModelName))
		}
		if patch.ThinkingEnabled != nil {
			sets = append(sets, "thinking_enabled = ?")
			args = append(args, sqlBoolToInt(*patch.ThinkingEnabled))
		}
		if patch.ThinkingEffort != nil {
			sets = append(sets, "thinking_effort = ?")
			args = append(args, strings.TrimSpace(*patch.ThinkingEffort))
		}
		if patch.PlanMode != nil {
			sets = append(sets, "plan_mode = ?")
			args = append(args, sqlBoolToInt(*patch.PlanMode))
		}
		if patch.AllowLiveTest != nil {
			sets = append(sets, "allow_live_test = ?")
			args = append(args, agent.NormalizedPermission(*patch.AllowLiveTest))
		}
		if patch.AllowSave != nil {
			sets = append(sets, "allow_save = ?")
			args = append(args, agent.NormalizedPermission(*patch.AllowSave))
		}
		if patch.TestBaseURL != nil {
			sets = append(sets, "test_base_url = ?")
			args = append(args, strings.TrimSpace(*patch.TestBaseURL))
		}
	}
	if clearAPIKey {
		sets = append(sets, "test_api_key = ''")
	} else if apiKey != nil && strings.TrimSpace(*apiKey) != "" {
		encrypted, err := s.codec.encrypt(strings.TrimSpace(*apiKey))
		if err != nil {
			return nil, err
		}
		sets = append(sets, "test_api_key = ?")
		args = append(args, encrypted)
	}
	args = append(args, id)
	if _, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return nil, err
	}
	return s.GetAgentSession(ctx, id)
}

// UpdateSessionState 实现 agent.Store：引擎状态增量写回。
func (s *Store) UpdateSessionState(ctx context.Context, id string, update agent.SessionStateUpdate) error {
	id = strings.TrimSpace(id)
	sets := []string{"updated_at = ?"}
	args := []any{nowString()}
	if update.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *update.Status)
	}
	if update.ClearPending {
		sets = append(sets, "pending_action = NULL")
	} else if update.PendingAction != nil {
		encoded, err := json.Marshal(update.PendingAction)
		if err != nil {
			return err
		}
		sets = append(sets, "pending_action = ?")
		args = append(args, string(encoded))
	}
	if len(update.DraftConfig) > 0 {
		sets = append(sets, "draft_config = ?")
		args = append(args, string(update.DraftConfig))
	}
	if update.DraftRestore != nil {
		sets = append(sets, "draft_restore = ?")
		args = append(args, string(update.DraftRestore))
	}
	if update.Plan != nil {
		encoded, err := json.Marshal(update.Plan)
		if err != nil {
			return err
		}
		sets = append(sets, "plan_json = ?")
		args = append(args, string(encoded))
	}
	if strings.TrimSpace(update.Title) != "" {
		sets = append(sets, "title = ?")
		args = append(args, strings.TrimSpace(update.Title))
	}
	if strings.TrimSpace(update.TestBaseURL) != "" {
		sets = append(sets, "test_base_url = ?")
		args = append(args, strings.TrimSpace(update.TestBaseURL))
	}
	if strings.TrimSpace(update.TestAPIKey) != "" {
		encrypted, err := s.codec.encrypt(strings.TrimSpace(update.TestAPIKey))
		if err != nil {
			return err
		}
		sets = append(sets, "test_api_key = ?")
		args = append(args, encrypted)
	}
	args = append(args, id)
	result, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return fmt.Errorf("agent session %q not found", id)
	}
	return nil
}

// AppendMessage 实现 agent.Store：追加消息（seq 单调递增）。
func (s *Store) AppendMessage(ctx context.Context, sessionID string, role string, content any, model string, usage json.RawMessage) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	encoded, err := json.Marshal(content)
	if err != nil {
		return 0, err
	}
	usageText := ""
	if len(usage) > 0 {
		usageText = string(usage)
	}
	// 单连接 SQLite：事务内读 max(seq)+写入。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM agent_messages WHERE session_id = ?`, sessionID).Scan(&next); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_messages(session_id, seq, role, content, model, usage_json, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, sessionID, next, role, string(encoded), model, usageText, nowString()); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET updated_at = ? WHERE id = ?`, nowString(), sessionID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// ListMessages 实现 agent.Store：按 seq 升序返回全部消息。
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]agent.Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, role, content, model, usage_json, created_at
		FROM agent_messages WHERE session_id = ? ORDER BY seq`, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []agent.Message{}
	for rows.Next() {
		var message agent.Message
		var content, created string
		var model, usage string
		if err := rows.Scan(&message.Seq, &message.Role, &content, &model, &usage, &created); err != nil {
			return nil, err
		}
		message.Content = json.RawMessage(content)
		message.Model = model
		if usage != "" {
			message.Usage = json.RawMessage(usage)
		}
		message.CreatedAt = parseTime(created)
		items = append(items, message)
	}
	return items, rows.Err()
}

// TruncateMessages 实现 agent.Store：删除 seq > afterSeq 的消息（含动作）。
func (s *Store) TruncateMessages(ctx context.Context, sessionID string, afterSeq int) error {
	sessionID = strings.TrimSpace(sessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_messages WHERE session_id = ? AND seq > ?`, sessionID, afterSeq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET updated_at = ?, pending_action = NULL, status = ? WHERE id = ?`,
		nowString(), agent.StatusIdle, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAgentSession 删除会话及其消息。
func (s *Store) DeleteAgentSession(ctx context.Context, id string) (bool, error) {
	id = strings.TrimSpace(id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM agent_sessions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_messages WHERE session_id = ?`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected > 0, nil
}

func newAgentSessionID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "as_" + fmt.Sprintf("%d", time.Now().UnixNano()) + "_" + hex.EncodeToString(raw), nil
}

var _ agent.Store = (*Store)(nil)

// ResetRunningSessions 把崩溃遗留的 running 会话复位为 idle（agent.Store
// 对账路径；waiting_approval 不动——待批动作仍可经审批恢复）。
func (s *Store) ResetRunningSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET status = ? WHERE status = ?`, agent.StatusIdle, agent.StatusRunning)
	return err
}
