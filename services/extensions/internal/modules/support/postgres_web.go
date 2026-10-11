package support

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type LocatedConversation struct {
	ShopID       string
	Conversation Conversation
	UnreadCount  int64
}
type LegacyIndex interface {
	FindConversation(context.Context, string) (LocatedConversation, error)
	ListForActor(context.Context, int64, string, string, bool, int, int) ([]LocatedConversation, error)
	MessagesBefore(context.Context, string, string, int64, int) ([]Message, error)
}

func (t *postgresTx) RateLimitTime() (int64, error) {
	var now int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT floor(extract(epoch from clock_timestamp())*1000)::bigint`).Scan(&now)
	return now, err
}

// NextMessageID is a global, durable sequence in this extension's existing
// JSONB command table. Allocation participates in the same message transaction;
// no PostgreSQL sequence or core/schema change is needed.
func (t *postgresTx) NextMessageID() (int64, error) {
	var n int64
	err := t.tx.QueryRowContext(t.ctx, `INSERT INTO lmm_support.commands(shop_id,id,body) VALUES ('@compat','message-sequence','{"value":1}') ON CONFLICT(shop_id,id) DO UPDATE SET body=jsonb_build_object('value',(lmm_support.commands.body->>'value')::bigint+1) RETURNING (body->>'value')::bigint`).Scan(&n)
	return n, err
}

func (p *Postgres) FindConversation(ctx context.Context, id string) (LocatedConversation, error) {
	if !store.Text(id, 128) {
		return LocatedConversation{}, store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT shop_id,body FROM lmm_support.conversations WHERE id=$1 LIMIT 2`, id)
	if err != nil {
		return LocatedConversation{}, err
	}
	defer rows.Close()
	var out LocatedConversation
	count := 0
	for rows.Next() {
		count++
		var body []byte
		if err = rows.Scan(&out.ShopID, &body); err != nil {
			return out, err
		}
		if err = json.Unmarshal(body, &out.Conversation); err != nil {
			return out, err
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if count == 0 {
		return out, store.ErrNotFound
	}
	if count != 1 {
		return out, store.ErrConflict
	}
	return out, nil
}
func (p *Postgres) ListForActor(ctx context.Context, actor int64, role, status string, unread bool, offset, limit int) ([]LocatedConversation, error) {
	if (role != "buyer" && role != "seller") || (status != "" && status != "open" && status != "resolved") {
		return nil, store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT c.shop_id,c.body,(SELECT count(*) FROM lmm_support.messages m WHERE m.shop_id=c.shop_id AND m.body->>'conversation_id'=c.id AND (m.body->>'author_user_id')::bigint<>$1 AND COALESCE((m.body->>'numeric_id')::bigint,split_part(m.id,':',2)::bigint)>COALESCE((c.body->>($2::text||'_read_id'))::bigint,0)) AS unread_count FROM lmm_support.conversations c WHERE c.body->$2::text->>'kind'='personal' AND (c.body->$2::text->>'id')::bigint=$1 AND ($3='' OR COALESCE((c.body->>'closed')::boolean,false)=($3='resolved')) AND (NOT $4 OR EXISTS(SELECT 1 FROM lmm_support.messages m WHERE m.shop_id=c.shop_id AND m.body->>'conversation_id'=c.id AND (m.body->>'author_user_id')::bigint<>$1 AND COALESCE((m.body->>'numeric_id')::bigint,split_part(m.id,':',2)::bigint)>COALESCE((c.body->>($2::text||'_read_id'))::bigint,0))) ORDER BY COALESCE(c.body->>'updated_at',c.body->>'created_at') DESC,c.id DESC OFFSET $5 LIMIT $6`, actor, role, status, unread, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocatedConversation{}
	for rows.Next() {
		var v LocatedConversation
		var body []byte
		if err = rows.Scan(&v.ShopID, &body, &v.UnreadCount); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &v.Conversation); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *postgresTx) MessageExists(conversation string, id int64) (bool, error) {
	var found bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM lmm_support.messages WHERE shop_id=$1 AND body->>'conversation_id'=$2 AND COALESCE((body->>'numeric_id')::bigint,split_part(id,':',2)::bigint)=$3)`, t.shop, conversation, id).Scan(&found)
	return found, err
}
func (p *Postgres) MessagesBefore(ctx context.Context, shop, id string, before int64, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT body FROM lmm_support.messages WHERE shop_id=$1 AND body->>'conversation_id'=$2 AND ($3::bigint=0 OR COALESCE((body->>'numeric_id')::bigint,split_part(id,':',2)::bigint)<$3) ORDER BY COALESCE((body->>'numeric_id')::bigint,split_part(id,':',2)::bigint) DESC LIMIT $4`, shop, id, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var v Message
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
