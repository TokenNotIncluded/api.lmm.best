from pathlib import Path
import hashlib
import json
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
changes = []
def replace(path, before, after):
    p = root / path
    data = p.read_text()
    assert data.count(before) == 1, path
    p.write_text(data.replace(before, after))
    changes.append(path)

replace('apps/api-go/controller/assistant.go',
    'then use the server-validated registration tools after the completed-turn threshold.',
    'then use the server-validated registration tools immediately, including in the first reply. There is no completed-turn threshold.')
replace('apps/api-go/model/assistant_registration_guard.go',
    'if err := tx.First(&profile, "user_id = ?", userID).Error; err != nil {\n\t\treturn nil, ErrAssistantRegistrationCheck\n\t}',
    'if err := tx.First(&profile, "user_id = ?", userID).Error; err != nil {\n\t\tif errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn nil, ErrAssistantRegistrationCheck\n\t\t}\n\t\treturn nil, err\n\t}')
replace('apps/api-go/model/assistant_registration_guard.go',
    'var current AssistantRegistrationCase\n\tif DB.First(&current, "user_id = ?", userID).Error == nil && current.State == "suspended" {\n\t\treturn "suspended"\n\t}\n\tsummary, err := GetAssistantRegistrationSummary(userID)\n\tif err != nil {\n\t\treturn "context_needed"\n\t}',
    'var current AssistantRegistrationCase\n\tif err := DB.First(&current, "user_id = ?", userID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {\n\t\treturn "unavailable"\n\t}\n\tif current.State == "suspended" {\n\t\treturn "suspended"\n\t}\n\tsummary, err := GetAssistantRegistrationSummary(userID)\n\tif errors.Is(err, ErrAssistantRegistrationCheck) {\n\t\treturn "context_needed"\n\t}\n\tif err != nil {\n\t\treturn "unavailable"\n\t}')
replace('apps/api-go/controller/assistant_registration_guard.go',
    'if !access.Granted {\n\t\tstate = model.RegistrationPublicState(user.Id)\n\t}\n\tcommon.ApiSuccess',
    'if !access.Granted {\n\t\tstate = model.RegistrationPublicState(user.Id)\n\t\tif user.Status != common.UserStatusEnabled {\n\t\t\tstate = "suspended"\n\t\t} else if user.TrustLevelOverride != nil && *user.TrustLevelOverride < 1 {\n\t\t\tstate = "held"\n\t\t}\n\t}\n\tif state == "unavailable" {\n\t\tcommon.ApiError(c, model.ErrAssistantRegistrationCheck)\n\t\treturn\n\t}\n\tcommon.ApiSuccess')
for path, addition in {
    'apps/api-go/model/assistant_registration_guard_test.go': '''
func TestRegistrationPublicStateDoesNotTurnDatabaseFailuresIntoMoreConversation(t *testing.T) {
	setupRegistrationGuard(t)
	user := guardUser(t, "public-state-errors")
	require.Equal(t, "ready", RegistrationPublicState(user.Id))
	require.NoError(t, DB.Migrator().DropTable(&AssistantRegistrationProfile{}))
	require.Equal(t, "unavailable", RegistrationPublicState(user.Id))
	require.NoError(t, DB.Migrator().DropTable(&AssistantRegistrationCase{}))
	require.Equal(t, "unavailable", RegistrationPublicState(user.Id))
}
''',
    'apps/api-go/controller/assistant_context_test.go': '''
func TestAssistantL1RulesDoNotRetainTheRetiredTurnThreshold(t *testing.T) {
	assert.Contains(t, assistantSystemRules, "grant_l1_access immediately, including in the first reply")
	assert.NotContains(t, assistantSystemRules, "after the completed-turn threshold")
	assert.Contains(t, assistantSystemRules, "never tell them to keep chatting")
	assert.Contains(t, assistantSystemRules, "offer human support or one retry")
}
''',
}.items():
    p = root / path
    p.write_text(p.read_text() + addition)
    changes.append(path)
expected = {
    'apps/api-go/controller/assistant.go': 'ae1b5a55e215d8efcfde8ad888933bd6cbaeb81009f419062bb6972216f45238',
    'apps/api-go/model/assistant_registration_guard.go': '200086ac2c772075d07e8cecd25f9f74aef3bcfa6b4222ee5eace67f797feffa',
    'apps/api-go/controller/assistant_registration_guard.go': '9c3d7d835ad4b76ad7639f3a3954ed29a830a330352ff94a40a9f81763949be7',
    'apps/api-go/model/assistant_registration_guard_test.go': '8ebbedce712705eb6031afedcc7463bebb7e4dbda65c7eaf69f9b13131459393',
    'apps/api-go/controller/assistant_context_test.go': '841a0a0d50e0ed5d42d1510a0c02f6c232119432b296a4bcbce85038cbfc0401',
}
for path, digest in expected.items():
    assert hashlib.sha256((root / path).read_bytes()).hexdigest() == digest, path
subprocess.run(['git', 'add', '--', *sorted(set(changes))], cwd=root, check=True)
p = root.parent / 'l0-paths.json'
p.write_text(json.dumps(sorted(set(json.loads(p.read_text()) + changes))))
print('Follow-up source hashes verified')
