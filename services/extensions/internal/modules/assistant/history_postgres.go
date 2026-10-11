// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

func historyError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	return access.ErrUnavailable
}

func (p *Postgres) RecordTurn(ctx context.Context, userID int64, turn CompletedTurn) (int64, error) {
	if userID <= 0 || turn.Now <= 0 || !access.ID(turn.ClientTurnID) || len(turn.ClientTurnID) > 64 || turn.ConversationID < 0 || turn.ConversationID > maxSafeHistoryID ||
		turn.UserContent == "" || turn.AnswerContent == "" || len([]rune(turn.UserContent)) > 4000 || len([]rune(turn.AnswerContent)) > 4000 {
		return 0, access.ErrInvalid
	}
	if turn.UserContent != redactAssistantHistoryBounded(turn.UserContent) || turn.AnswerContent != redactAssistantHistoryBounded(turn.AnswerContent) ||
		turn.InputHash != sha256.Sum256([]byte(turn.UserContent)) || turn.AnswerHash != sha256.Sum256([]byte(turn.AnswerContent)) {
		return 0, access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, access.ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "lmm_assistant_history:"+strconv.FormatInt(userID, 10)); err != nil {
		return 0, access.ErrUnavailable
	}
	var priorID int64
	var inputHash, answerHash []byte
	err = tx.QueryRowContext(ctx, `SELECT conversation_id,input_hash,answer_hash FROM lmm_assistant.turn_receipts WHERE user_id=$1 AND client_turn_id=$2`, userID, turn.ClientTurnID).Scan(&priorID, &inputHash, &answerHash)
	if err == nil {
		if (turn.ConversationID != 0 && turn.ConversationID != priorID) || !bytes.Equal(inputHash, turn.InputHash[:]) || !bytes.Equal(answerHash, turn.AnswerHash[:]) {
			return 0, access.ErrConflict
		}
		return priorID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, access.ErrUnavailable
	}
	id := turn.ConversationID
	if id == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO lmm_assistant.conversations(user_id,title,last_message_preview,created_at,updated_at) VALUES($1,$2,$3,$4,$4) RETURNING id`,
			userID, assistantConversationTitle(turn.UserContent), assistantConversationTitle(turn.AnswerContent), turn.Now).Scan(&id)
	} else {
		var restricted int64
		err = tx.QueryRowContext(ctx, `SELECT restricted_at FROM lmm_assistant.conversations WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, userID).Scan(&restricted)
		if err == nil && restricted != 0 {
			return 0, access.ErrConflict
		}
	}
	if err != nil {
		return 0, historyError(err)
	}
	var sequence int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM lmm_assistant.history_messages WHERE conversation_id=$1`, id).Scan(&sequence); err != nil {
		return 0, access.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO lmm_assistant.history_messages(conversation_id,sequence,role,content,created_at) VALUES($1,$2,'user',$3,$5),($1,$2+1,'assistant',$4,$5)`,
		id, sequence, turn.UserContent, turn.AnswerContent, turn.Now)
	if err != nil {
		return 0, access.ErrUnavailable
	}
	// Keep the latest bounded transcript, retaining replay receipts separately.
	_, err = tx.ExecContext(ctx, `WITH ranked AS (
        SELECT id,row_number() OVER(ORDER BY sequence DESC) AS n,
          sum(octet_length(content)) OVER(ORDER BY sequence DESC) AS bytes
        FROM lmm_assistant.history_messages WHERE conversation_id=$1
      ) DELETE FROM lmm_assistant.history_messages WHERE id IN (
        SELECT id FROM ranked WHERE n>128 OR bytes>262144)`, id)
	if err != nil {
		return 0, access.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `UPDATE lmm_assistant.conversations SET last_message_preview=$1,updated_at=GREATEST(updated_at,$2) WHERE id=$3 AND user_id=$4`,
		assistantConversationTitle(turn.AnswerContent), turn.Now, id, userID)
	if err != nil {
		return 0, access.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO lmm_assistant.turn_receipts(user_id,client_turn_id,conversation_id,input_hash,answer_hash) VALUES($1,$2,$3,$4,$5)`,
		userID, turn.ClientTurnID, id, turn.InputHash[:], turn.AnswerHash[:])
	if err != nil || tx.Commit() != nil {
		return 0, access.ErrUnavailable
	}
	return id, nil
}

const conversationColumns = `id,title,last_message_preview,created_at,updated_at,archived_at,restricted_at`

type rowScanner interface{ Scan(...any) error }

func scanConversation(row rowScanner) (Conversation, error) {
	var c Conversation
	if err := row.Scan(&c.ID, &c.Title, &c.LastMessagePreview, &c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt, &c.RestrictedAt); err != nil {
		return c, historyError(err)
	}
	c.Owner, c.PrivacyNotice = "self", HistoryPrivacyNotice
	return c, nil
}

func (p *Postgres) ListHistory(ctx context.Context, userID int64, archived bool, after HistoryPosition, limit int) ([]Conversation, error) {
	if userID <= 0 || limit < 1 || limit > 101 || after.ID < 0 || after.UpdatedAt < 0 {
		return nil, access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT `+conversationColumns+` FROM lmm_assistant.conversations WHERE user_id=$1 AND (archived_at<>0)=$2 AND ($3::bigint=0 OR (updated_at,id)<($3,$4)) ORDER BY updated_at DESC,id DESC LIMIT $5`,
		userID, archived, after.UpdatedAt, after.ID, limit)
	if err != nil {
		return nil, access.ErrUnavailable
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		item, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, access.ErrUnavailable
	}
	return items, nil
}

func (p *Postgres) ReadHistory(ctx context.Context, userID, id int64, limit int) (Conversation, []HistoryMessage, error) {
	if userID <= 0 || id <= 0 || id > maxSafeHistoryID || limit < 1 || limit > 100 {
		return Conversation{}, nil, access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Conversation{}, nil, access.ErrUnavailable
	}
	defer tx.Rollback()
	item, err := scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM lmm_assistant.conversations WHERE id=$1 AND user_id=$2`, id, userID))
	if err != nil {
		return item, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,role,content,created_at FROM (
        SELECT id,sequence,role,content,created_at FROM lmm_assistant.history_messages
        WHERE conversation_id=$1 ORDER BY sequence DESC LIMIT $2
      ) recent ORDER BY sequence`, id, limit)
	if err != nil {
		return item, nil, access.ErrUnavailable
	}
	messages := []HistoryMessage{}
	for rows.Next() {
		var m HistoryMessage
		if rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt) != nil {
			rows.Close()
			return item, nil, access.ErrUnavailable
		}
		messages = append(messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit() != nil {
		return item, nil, access.ErrUnavailable
	}
	return item, messages, nil
}

func (p *Postgres) ArchiveHistory(ctx context.Context, userID, id int64, archived bool, now int64) (Conversation, error) {
	if userID <= 0 || id <= 0 || id > maxSafeHistoryID || now <= 0 {
		return Conversation{}, access.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	value := int64(0)
	if archived {
		value = now
	}
	row := p.db.QueryRowContext(ctx, `UPDATE lmm_assistant.conversations SET archived_at=$1 WHERE id=$2 AND user_id=$3 AND (archived_at<>0)<>$4 RETURNING `+conversationColumns, value, id, userID, archived)
	item, err := scanConversation(row)
	if errors.Is(err, access.ErrNotFound) {
		var exists bool
		if e := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM lmm_assistant.conversations WHERE id=$1 AND user_id=$2)`, id, userID).Scan(&exists); e != nil {
			return Conversation{}, access.ErrUnavailable
		}
		if exists {
			return Conversation{}, access.ErrConflict
		}
	}
	return item, err
}

func (p *Postgres) ClickPreset(ctx context.Context, id string) error {
	if _, ok := requiredPromptPresetCopy[id]; !ok {
		return access.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := p.db.ExecContext(ctx, `UPDATE lmm_assistant.preset_stats SET clicks=clicks+1 WHERE preset_id=$1 AND clicks<9007199254740991`, id)
	if err != nil {
		return access.ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return access.ErrLimit
	}
	return nil
}
