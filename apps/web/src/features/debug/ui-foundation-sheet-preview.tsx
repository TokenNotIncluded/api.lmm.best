/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState } from 'react'

import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { useTheme } from '@/context/theme-provider'

const cases = [
  { id: 'right-default', side: 'right', wide: false },
  { id: 'left-default', side: 'left', wide: false },
  { id: 'right-wide', side: 'right', wide: true },
  { id: 'left-wide', side: 'left', wide: true },
] as const

// Debug-only sizing fixture: real Sheet primitives, Tailwind and shared drawer
// layout. The wide case mirrors the channel editor's 5xl / 13rem column contract.
// It does not fetch channel data, reveal credentials or persist changes.
export function UIFoundationSheetPreview() {
  const { resolvedTheme, setTheme } = useTheme()
  const [saved, setSaved] = useState('')

  return (
    <main
      className='bg-background text-foreground min-h-dvh space-y-4 p-4'
      data-testid='sheet-sizing-preview'
    >
      <h1>侧边编辑面板</h1>
      <Button
        data-testid='sheet-theme-toggle'
        onClick={() => setTheme(resolvedTheme === 'dark' ? 'light' : 'dark')}
      >
        切换主题
      </Button>
      <output data-testid='sheet-saved'>{saved}</output>
      <div className='flex flex-wrap gap-3'>
        {cases.map(({ id, side, wide }) => (
          <Sheet key={id}>
            <SheetTrigger
              data-testid={`open-${id}`}
              render={<Button variant='outline' />}
            >
              {id}
            </SheetTrigger>
            <SheetContent
              side={side}
              data-testid={id}
              className={
                wide
                  ? sideDrawerContentClassName('sm:max-w-5xl')
                  : 'gap-0 p-0'
              }
            >
              <SheetHeader className={sideDrawerHeaderClassName()}>
                <SheetTitle>编辑配置</SheetTitle>
                <SheetDescription>本地布局回归示例</SheetDescription>
              </SheetHeader>
              <form
                id={`form-${id}`}
                className={sideDrawerFormClassName()}
                onSubmit={(event) => {
                  event.preventDefault()
                  setSaved(id)
                }}
              >
                <div
                  className={
                    wide
                      ? 'grid gap-5 lg:grid-cols-[13rem_minmax(0,1fr)] lg:items-start'
                      : 'grid gap-5'
                  }
                >
                  {wide && (
                    <aside className='border-border rounded-lg border p-4'>
                      基本信息 · 凭证 · 模型与分组
                    </aside>
                  )}
                  <div className='min-w-0 space-y-5' data-testid='sheet-fields'>
                    {Array.from({ length: 12 }, (_, index) => (
                      <div key={index} className='space-y-2'>
                        <Label htmlFor={`${id}-field-${index}`}>
                          {index === 0 ? 'API 地址' : `配置 ${index}`}
                        </Label>
                        <Input
                          id={`${id}-field-${index}`}
                          defaultValue={
                            index === 0 ? 'https://api.example.test/v1' : ''
                          }
                        />
                      </div>
                    ))}
                    <Label htmlFor={`${id}-note`}>备注</Label>
                    <Textarea id={`${id}-note`} data-testid='sheet-note' />
                  </div>
                </div>
              </form>
              <SheetFooter className={sideDrawerFooterClassName()}>
                <SheetClose render={<Button variant='outline' />}>
                  取消
                </SheetClose>
                <Button type='submit' form={`form-${id}`}>
                  保存
                </Button>
              </SheetFooter>
            </SheetContent>
          </Sheet>
        ))}
      </div>
    </main>
  )
}
