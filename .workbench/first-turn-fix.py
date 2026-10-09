from pathlib import Path
import sys
r = Path(sys.argv[1]).resolve()
def edit(path, before, after):
    p = r / path
    s = p.read_text()
    assert s.count(before) == 1, (path, s.count(before))
    p.write_text(s.replace(before, after))
f = 'apps/api-go/model/developer_access_request.go'
edit(f, '''type AssistantDeveloperAccessGrant struct {
	Request        *DeveloperAccessRequest
	CompletedTurns int
	Activated      bool
}''', '''type AssistantDeveloperAccessGrant struct {
	Request        *DeveloperAccessRequest
	ConversationID int64
	CompletedTurns int
	Activated      bool
}''')
p = r / f
s = p.read_text()
a = s.index('func GrantAssistantDeveloperAccess(')
b = s.index('\nfunc ', a + 5)
t = s[a:b].replace('conversationID <= 0', 'conversationID < 0')
old = '''		completedTurns, err := countCompletedAssistantConversationTurnsWithTx(tx, userID, conversationID, true)
		if err != nil {
			return err
		}
		result.CompletedTurns = completedTurns'''
new = '''		completedTurns := 0
		if conversationID > 0 {
			var err error
			completedTurns, err = countCompletedAssistantConversationTurnsWithTx(tx, userID, conversationID, true)
			if err != nil {
				return err
			}
		}
		result.ConversationID = conversationID
		result.CompletedTurns = completedTurns'''
assert old in t
t = t.replace(old, new)
old = '''		now := common.GetTimestamp()
		auditNote := fmt.Sprintf'''
new = '''		now := common.GetTimestamp()
		if conversationID == 0 {
			// A successful first-turn action needs an owned conversation before
			// the answer is written. Create it in this transaction so a failed
			// risk check or audit write never leaves an empty conversation.
			conversation := AssistantConversation{
				UserId: userID, Title: assistantConversationTitle(normalizedReason),
				LastMessagePreview: assistantConversationTitle(normalizedReason),
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
			conversationID = conversation.Id
			result.ConversationID = conversationID
		}
		auditNote := fmt.Sprintf'''
assert old in t
t = t.replace(old, new)
p.write_text(s[:a] + t + s[b:])
f = 'apps/api-go/controller/assistant_agent.go'
edit(f, '''	if conversationID <= 0 {
		return map[string]any{"ok": false, "status": "context_unavailable", "error": "an owned conversation is required; retry or contact support, not extra messages"}
	}''', '''	if conversationID < 0 || (conversationID == 0 && strings.TrimSpace(c.GetString("assistant_history_latest_message")) == "") {
		return map[string]any{"ok": false, "status": "context_unavailable", "error": "a current user message is required; retry or contact support, not extra messages"}
	}''')
edit(f, '''	status := "already_active"
	if grant.Activated {''', '''	if grant.ConversationID > 0 {
		c.Set("assistant_history_conversation_id", grant.ConversationID)
	}
	status := "already_active"
	if grant.Activated {''')
f = 'apps/api-go/model/assistant_direct_l1_grant_test.go'
p = r / f
s = p.read_text()
a = s.index('func TestAssistantDirectL1GrantConcurrentCallsPostgres(')
b = s.find('\nfunc ', a + 5)
b = len(s) if b < 0 else b
t = s[a:b]
old = '''	conversation, err := PrepareAssistantConversation(user.Id, 0, "first")
	require.NoError(t, err)
	recordAssistantDirectL1Turns(t, user.Id, conversation.Id, 3)
'''
assert old in t
t = t.replace(old, '').replace('user.Id, conversation.Id,', 'user.Id, 0,')
old = '\tassert.EqualValues(t, 1, archives)'
assert old in t
t = t.replace(old, old + '''
	var conversations int64
	require.NoError(t, db.Model(&AssistantConversation{}).Where("user_id = ?", user.Id).Count(&conversations).Error)
	assert.EqualValues(t, 1, conversations)''')
s = s[:a] + t + s[b:]
s += '''
func TestAssistantDirectL1GrantCreatesFirstConversationAtomically(t *testing.T) {
	user, existing := setupAssistantDirectL1GrantTest(t)
	require.NoError(t, DB.Delete(existing).Error)
	grant, err := GrantAssistantDeveloperAccess(user.Id, 0, "制作开源软件", "")
	require.NoError(t, err)
	require.True(t, grant.Activated)
	require.Positive(t, grant.ConversationID)
	assert.Zero(t, grant.CompletedTurns)
	var conversation AssistantConversation
	require.NoError(t, DB.First(&conversation, grant.ConversationID).Error)
	assert.Equal(t, user.Id, conversation.UserId)
	assert.Equal(t, "制作开源软件", conversation.Title)
}

func TestAssistantDirectL1GrantRollsBackFirstConversationWhenAuditFails(t *testing.T) {
	user, existing := setupAssistantDirectL1GrantTest(t)
	require.NoError(t, DB.Delete(existing).Error)
	require.NoError(t, DB.Migrator().DropTable(&DeveloperAccessRecommendationArchive{}))
	_, err := GrantAssistantDeveloperAccess(user.Id, 0, "制作开源软件", "")
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&AssistantConversation{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.Model(&DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, DB.First(user, user.Id).Error)
	assert.Zero(t, user.ConsoleActivatedAt)
}
'''
p.write_text(s)
print('First-turn atomic conversation repair prepared')
