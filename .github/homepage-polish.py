from pathlib import Path
import re

p = Path('apps/web/src/features/forge/forge-home.css')
s = p.read_text()

def replace(old, new, count=1):
    global s
    assert s.count(old) == count, (old[:90], s.count(old), count)
    s = s.replace(old, new)

def rule(selector, changes):
    global s
    matches = list(re.finditer(r'(?m)^' + re.escape(selector) + r' \{([^{}]*)\}', s))
    assert len(matches) == 1, (selector, len(matches))
    m = matches[0]
    body = m[1]
    for old, new in changes:
        assert body.count(old) == 1, (selector, old)
        body = body.replace(old, new)
    s = s[:m.start(1)] + body + s[m.end(1):]

replace('--home-stage-ground: var(--card);', '--home-stage-ground: var(--background);')
rule('.lmm-cinema', [('width: min(calc(100% - 3rem), 100rem);', 'width: min(calc(100% - 2 * var(--home-gutter)), 96rem);'), ('margin: 1rem auto 0;', 'margin: 0.5rem auto 0;')])
replace('  border-radius: 1.25rem;\n  border: 1px solid color-mix(in oklch, var(--home-stage-ink) 10%, transparent);', '  border-radius: 0;\n  border: 0;')
replace('  box-shadow: 0 2.8rem 7rem -5.5rem\n    color-mix(in oklch, var(--foreground) 38%, transparent);', '  box-shadow: none;')
replace('color-mix(in oklch, var(--forge-clay) 10%, transparent)', 'color-mix(in oklch, var(--forge-clay) 4%, transparent)')
replace('color-mix(in oklch, var(--forge-sage) 8%, transparent)', 'color-mix(in oklch, var(--forge-sage) 3%, transparent)')
replace('color-mix(in oklch, var(--forge-clay) 9%, transparent)', 'color-mix(in oklch, var(--forge-clay) 4%, transparent)')
replace('color-mix(in oklch, var(--forge-sage) 7%, transparent)', 'color-mix(in oklch, var(--forge-sage) 3%, transparent)')
rule('.lmm-token-cloud > [data-token-particle]', [('font-weight: 700;', 'font-weight: 500;'), ('text-shadow: 0 0 1.2rem color-mix(in oklch, currentColor 20%, transparent);', 'text-shadow: none;')])
replace('height: 440svh;', 'height: 340svh;')
replace('  height: calc(100svh - 6rem);\n  min-height: 32rem;\n  max-height: 64rem;', '  height: calc(100svh - 7rem);\n  min-height: 32rem;\n  max-height: 52rem;')
rule('.lmm-scene-panel', [('width: min(39%, 34rem);', 'width: min(39%, 30rem);')])
replace('  font-size: clamp(2.25rem, 4.2vw, 4.25rem);\n  font-weight: 500;\n  line-height: 1.04;', '  font-size: clamp(2.25rem, 3.7vw, 3.5rem);\n  font-weight: 500;\n  line-height: 1.2;')
replace('font-size: clamp(2.2rem, 3.6vw, 3.8rem);', 'font-size: clamp(2rem, 3.2vw, 3.2rem);')
replace('font-size: clamp(0.94rem, 1.3vw, 1.15rem);', 'font-size: clamp(0.94rem, 1.1vw, 1rem);')
rule('.lmm-access-note', [('padding: 1.1rem 0 0;', 'padding: 0;'), ('border-top: 1px solid var(--home-rule);', 'border-top: 0;')])
rule('.lmm-intro-actions [data-slot=\'button\']', [('border-radius: 0.6rem;', 'border-radius: 999px;')])
rule('.lmm-core-steps', [('bottom: 1.25rem;', 'bottom: calc(4rem + env(safe-area-inset-bottom, 0px));'), ('gap: 0.25rem;', 'gap: 0.6rem;'), ('font-size: 0.7rem;', 'font-size: 0.75rem;')])
rule('.lmm-core-steps li', [('border-top: 1px solid var(--home-rule);', 'border-top: 0;\n  border-bottom: 1px solid transparent;')])
rule('.lmm-core-steps [data-active]', [('border-top-color: var(--foreground);', 'border-bottom-color: var(--home-accent);')])
replace('  border: 1px solid color-mix(in oklch, var(--home-stage-ink) 14%, transparent);\n  border-radius: 999px;\n  background: color-mix(in oklch, var(--home-stage-ground) 80%, transparent);', '  border: 0;\n  border-radius: 999px;\n  background: transparent;')
rule('.lmm-motion-toggle', [('border: 1px solid color-mix(in oklch, var(--home-stage-ink) 14%, transparent);', 'border: 0;'), ('background: color-mix(in oklch, var(--home-stage-ground) 80%, transparent);', 'background: transparent;')])
rule('.lmm-protocols > span', [('border: 1px solid var(--home-rule);', 'border: 0;')])
rule('.lmm-assistant-surface', [('background: var(--card);', 'background: transparent;'), ('border: 1px solid var(--home-rule);', 'border: 0;')])
rule('.lmm-home .forge-home-input', [('border-radius: 0.55rem;', 'border-radius: 1.9rem;')])
replace('    height: 380svh;', '    height: 340svh;')
replace('    top: 45%;', '    top: 43%;')
replace('    font-size: 0.7rem;\n    margin-top: 1rem;\n    padding-top: 0.8rem;', '    font-size: 0.7rem;\n    margin-top: 1rem;\n    padding-top: 0;')
replace('    bottom: 1rem;\n    font-size: 0.55rem;', '    bottom: calc(4rem + env(safe-area-inset-bottom, 0px));\n    font-size: 0.7rem;')
replace('border-bottom: 1px solid var(--home-rule);\n  -webkit-mask-image: none;', 'border-bottom: 0;\n  -webkit-mask-image: none;')
replace('border-bottom: 1px solid var(--home-rule);\n    -webkit-mask-image: none;', 'border-bottom: 0;\n    -webkit-mask-image: none;', 2)
replace('    height: 42%;', '    height: 40%;')
p.write_text(s)
