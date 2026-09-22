# One-shot mechanical adapters; this helper never accesses credentials.
from pathlib import Path
import json, re, os, hashlib
root=Path('apps/web'); ui=root/'src/components/ui'
header=(ui/'drawer.tsx').read_text().split("'use client'")[0]
s=json.loads(Path(os.environ['UI_LUMA_DRAWER']).read_text())['files'][0]['content']
assert hashlib.sha256(s.encode()).hexdigest()=='012a706d54985284a8fd2a8d641ce59bfa36683a03d3b57ea3feb20006bbe464'
s=s.replace('"use client"\n\n','').replace('import { cn } from "cn"','import { cn } from "@/lib/utils"')
s=s.replace('showSwipeHandle = false','showSwipeHandle = true').replace('bg-black/30','bg-foreground/20 dark:bg-background/70').replace('duration-450','duration-300')
s=re.sub(r' \[--drawer-stacked-shadow:[^\]]+\]', ' [--drawer-stacked-shadow:var(--shadow-xl)]',s)
s=s.replace('<DrawerPortal data-slot="drawer-portal">','<DrawerPrimitive.VirtualKeyboardProvider>\n<DrawerPortal data-slot="drawer-portal">').replace('</DrawerPortal>','</DrawerPortal>\n</DrawerPrimitive.VirtualKeyboardProvider>')
s=s.replace('overflow-hidden overscroll-contain','overflow-y-auto overscroll-contain').replace('"cn-font-heading text-base font-medium text-foreground"','"text-base font-semibold text-foreground"')
s=s.replace('className\n      )}', '"motion-reduce:transition-none",\nclassName\n      )}').replace('            className\n          )}', '"motion-reduce:transition-none",\nclassName\n          )}')
s=s.replace('group-data-swiping/drawer-popup:select-none"','group-data-swiping/drawer-popup:select-none motion-reduce:transition-none"')
(ui/'drawer.tsx').write_text(header+s)
p=root/'src/features/usage-logs/components/logs-filter-toolbar.tsx';s=p.read_text()
start=s.index('              <DrawerTrigger asChild>');end=s.index('              </DrawerTrigger>',start)+len('              </DrawerTrigger>')
block=s[start:end].replace('<DrawerTrigger asChild>\n                <Button','<DrawerTrigger\n                render={<Button').replace("                  )}\n                >", "                  )}\n                />}\n              >").replace('                </Button>\n              </DrawerTrigger>','              </DrawerTrigger>')
p.write_text(s[:start]+block+s[end:])
p=root/'src/features/auth/otp/components/otp-form.tsx';s=p.read_text()
assert 'maxLength={OTP_LENGTH}\n                    {...field}' in s
s=s.replace('maxLength={OTP_LENGTH}\n                    {...field}', 'length={OTP_LENGTH}\nname={field.name}\nvalue={field.value}\nonValueChange={field.onChange}\nonBlur={field.onBlur}\ndisabled={isLoading}\nautoSubmit={false}')
s=s.replace('containerClassName=\'justify-between sm:[&>[data-slot="input-otp-group"]>div]:w-12\'',"containerClassName='justify-between'").replace('<InputOTPSlot index={0} />','<InputOTPSlot index={0} ref={field.ref} />');p.write_text(s)
p=root/'src/debug-main.tsx';s=p.read_text();assert "void import('./main')" in s
p.write_text(s.replace("void import('./main')", "if (new URLSearchParams(window.location.search).get('ui_review') === '1') {\n  void import('@/features/debug/ui-foundation-entry')\n} else {\n  void import('./main')\n}"))
for p in ui.glob('*.tsx'):
 s=p.read_text()
 if p.name=='button.tsx':s=s.replace('rounded-md in-data-[slot=button-group]:rounded-lg ','').replace('rounded-md text-[0.8rem] in-data-[slot=button-group]:rounded-lg ','')
 if p.name=='switch.tsx':s=re.sub(r'group-data-\[size=(default|sm)\]/switch:(?:size-[34]|data-checked:translate-x-\[calc\(100%-2px\)\]|data-unchecked:translate-x-0) ?', '',s)
 if p.name=='card.tsx':
  s=s.replace('data-[size=sm]:gap-3 data-[size=sm]:py-3 ','').replace('group-data-[size=sm]/card:px-3 ','').replace('group-data-[size=sm]/card:[.border-b]:pb-3 ','').replace("variant === 'paper' && 'border-0 shadow-none'","variant === 'paper' && 'border-0 ring-0 shadow-none'")
 if p.name=='slider.tsx':s=s.replace('relative block size-3 shrink-0','relative block shrink-0')
 if p.name=='native-select.tsx':s=s.replace(' h-9 w-full',' h-11 w-full').replace('data-[size=sm]:h-8 data-[size=sm]:rounded-md','data-[size=sm]:h-11')
 if p.name=='select.tsx':s=s.replace('data-[size=sm]:rounded-md ','')
 s=s.replace('bg-black/30','bg-foreground/20 dark:bg-background/70')
 if p.name in ['dialog.tsx','alert-dialog.tsx']:s=s.replace('bg-muted/50 -mx-4 -mb-4 ','').replace('rounded-b-xl border-t p-4 ','pt-2 ')
 if p.name=='button-group.tsx':
  s=s.replace('*:data-slot:rounded-r-none [&>[data-slot]:not(:has(~[data-slot]))]:rounded-r-lg! [&>[data-slot]~[data-slot]]:rounded-l-none','*:data-slot:rounded-none [&>[data-slot]:first-child]:rounded-l-4xl [&>[data-slot]:last-child]:rounded-r-4xl')
  s=s.replace('*:data-slot:rounded-b-none [&>[data-slot]:not(:has(~[data-slot]))]:rounded-b-lg! [&>[data-slot]~[data-slot]]:rounded-t-none','*:data-slot:rounded-none [&>[data-slot]:first-child]:rounded-t-4xl [&>[data-slot]:last-child]:rounded-b-4xl').replace('gap-2 rounded-lg border bg-muted','gap-2 rounded-4xl border bg-muted')
 p.write_text(s)
components=sorted(set(json.loads(Path(os.environ['UI_LUMA_AUDIT']).read_text())+['drawer.tsx','input-otp.tsx']))
for f in components:
 p=ui/f;s=p.read_text();notice='// Luma visual recipes adapted from shadcn/ui (MIT); see LUMA-LICENSE.txt.\n'
 if notice.strip() not in s:
  pos=s.index('*/')+2;s=s[:pos]+'\n'+notice+s[pos:].lstrip('\n');p.write_text(s)
manifest={'checkedOn':'2026-09-22','style':'base-luma','baseUI':'1.8.0','previousStyle':'base-nova','upstreamCSS':{'url':'https://github.com/shadcn-ui/ui/blob/main/apps/v4/registry/styles/style-luma.css','gitBlob':'739e8f9260ef9e82f31bb42f5d579df13ada2787','sha256':'a698210162ca2d3385142a47334f22985831b1e91716c2d3175828d928a59707'},'components':components,'migrations':{'drawer':'Vaul to @base-ui/react/drawer; explicit render triggers','input-otp':'input-otp to @base-ui/react/otp-field; native slots and explicit RHF adapter'},'retained':['cmdk','sonner','@tanstack/react-table','react-hook-form','react-day-picker'],'license':'LUMA-LICENSE.txt'}
(ui/'luma-migration.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n')
license=next(Path('node_modules/shadcn').glob('LICENSE*'))
(ui/'LUMA-LICENSE.txt').write_text(license.read_text())
