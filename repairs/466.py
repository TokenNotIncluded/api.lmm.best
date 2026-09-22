from pathlib import Path
import subprocess
p=Path.cwd()/'apps/web/src/features/system-settings/content/assistant-settings-section.test.tsx'
s=p.read_text()
a=s.index("  test('loads model IDs for the selected group automatically and permits an explicit refresh', async () => {")
b=s.index('\n  })\n})',a)+len('\n  })')
fn=s[a:b]
fn=fn.replace("    const originalGet = api.get",'''    const waitForState = async (ready: () => boolean) => {
      const deadline = Date.now() + 5000
      while (!ready() && Date.now() < deadline) {
        await act(flushEffects)
      }
      assert.ok(ready(), 'model request and rendered control must settle')
    }
    const originalGet = api.get''',1)
fn=fn.replace('      await act(flushEffects)\n      assert.equal(modelRequests.length, 1)', '      await waitForState(() => modelRequests.length === 1)\n      assert.equal(modelRequests.length, 1)')
fn=fn.replace('      assert.ok(groupTrigger)\n','      assert.ok(groupTrigger)\n      await waitForState(() => !groupTrigger.disabled)\n')
fn=fn.replace('      const domesticOption = [','''      await waitForState(() =>
        [...document.querySelectorAll('[role="option"]')].some((option) =>
          option.textContent?.includes('国产')
        )
      )
      const domesticOption = [''')
fn=fn.replace('      assert.equal(getModelListButton.disabled, false)', '''      await waitForState(
        () => modelRequests.length === 2 && !getModelListButton.disabled
      )
      assert.equal(getModelListButton.disabled, false)''',1)
fn=fn.replace("      assert.deepEqual(modelRequests, Array(3).fill('/api/assistant/models'))",'''      await waitForState(
        () => modelRequests.length === 3 && !getModelListButton.disabled
      )
      assert.deepEqual(modelRequests, Array(3).fill('/api/assistant/models'))''')
fn=fn.replace('      const modelOption = [','''      await waitForState(() =>
        [...document.querySelectorAll('[role="option"]')].some((option) =>
          option.textContent?.includes('deepseek-v4-flash-0731')
        )
      )
      const modelOption = [''')
s=s[:a]+fn+s[b:]
p.write_text(s)
subprocess.run(['bun','run','--filter','@lmm/web','format'],check=True)
