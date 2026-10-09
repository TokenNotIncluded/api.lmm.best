import json
import subprocess
from pathlib import Path
import sys

root = Path(sys.argv[1]).resolve()
missing = object()
def merge(base, ours, theirs, at):
    if ours == theirs: return ours
    if ours == base: return theirs
    if theirs == base: return ours
    if isinstance(ours, dict) and isinstance(theirs, dict) and (isinstance(base, dict) or base is missing):
        previous = base if isinstance(base, dict) else {}
        result = {}
        for key in dict.fromkeys([*ours, *theirs]):
            value = merge(previous.get(key, missing), ours.get(key, missing), theirs.get(key, missing), f'{at}/{key}')
            if value is not missing: result[key] = value
        return result
    raise RuntimeError(f'Overlapping semantic change requires review: {at}')

conflicts = subprocess.check_output(['git','diff','--name-only','--diff-filter=U'], cwd=root, text=True).splitlines()
for path in conflicts:
    if not path.startswith('apps/web/src/i18n/locales/') or not path.endswith('.json'):
        raise RuntimeError(f'Non-locale conflict requires review: {path}')
    values = [json.loads(subprocess.check_output(['git','show',f':{stage}:{path}'], cwd=root)) for stage in (1,2,3)]
    combined = merge(*values, path)
    (root/path).write_text(json.dumps(combined, ensure_ascii=False, indent=2)+'\n')
    subprocess.run(['git','add','--',path],cwd=root,check=True)
print('Merged independent locale changes:', conflicts)
