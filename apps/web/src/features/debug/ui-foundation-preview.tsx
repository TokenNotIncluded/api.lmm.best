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
import { Check, Copy, Moon, Plus, Sun } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogTrigger, DialogClose, DialogFooter } from '@/components/ui/dialog'
import { Drawer, DrawerContent, DrawerHeader, DrawerTitle, DrawerDescription, DrawerTrigger, DrawerClose, DrawerFooter } from '@/components/ui/drawer'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { InputOTP, InputOTPGroup, InputOTPSlot } from '@/components/ui/input-otp'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger, TooltipProvider } from '@/components/ui/tooltip'
import { useTheme } from '@/context/theme-provider'

function PreviewOTP() {
  const form = useForm<{ otp: string }>({ defaultValues: { otp: '' } })
  const [submits, setSubmits] = useState(0)
  return <Form {...form}>
    <form data-testid='otp-preview-form' className='space-y-5' onSubmit={form.handleSubmit(() => setSubmits(n => n + 1))}>
      <FormField control={form.control} name='otp' rules={{ required: '请输入验证码', minLength: { value: 6, message: '请输入六位验证码' } }} render={({ field }) => (
        <FormItem>
          <FormLabel>验证码</FormLabel>
          <FormControl>
            <InputOTP length={6} value={field.value} name={field.name} onValueChange={field.onChange} onBlur={field.onBlur} autoSubmit={false}>
              <InputOTPGroup className='gap-1.5'>
                {Array.from({ length: 6 }, (_, i) => <InputOTPSlot key={i} index={i} ref={i === 0 ? field.ref : undefined} className='size-9 sm:size-10' />)}
              </InputOTPGroup>
            </InputOTP>
          </FormControl>
          <FormMessage />
        </FormItem>
      )} />
      <div className='flex items-center justify-between gap-3'>
        <Button type='submit'>验证</Button>
        <output data-testid='otp-submit-count' className='text-muted-foreground text-xs'>{submits}</output>
      </div>
    </form>
  </Form>
}

function PreviewOverlays() {
  const [selection, setSelection] = useState('standard')
  return <div className='flex flex-wrap gap-3'>
    <Dialog>
      <DialogTrigger render={<Button variant='outline' />}>打开弹窗</DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>连接设置</DialogTitle><DialogDescription>仅用于本地交互检查，不会保存到服务器。</DialogDescription></DialogHeader>
        <Label htmlFor='modal-name'>名称</Label><Input id='modal-name' placeholder='开发环境' />
        <Select value={selection} onValueChange={v => v && setSelection(v)}>
          <SelectTrigger aria-label='连接模式'><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value='standard'>标准连接</SelectItem><SelectItem value='fast'>低延迟连接</SelectItem></SelectContent>
        </Select>
        <DialogFooter><DialogClose render={<Button variant='outline' />}>取消</DialogClose><DialogClose render={<Button />}>完成</DialogClose></DialogFooter>
      </DialogContent>
    </Dialog>
    <Drawer>
      <DrawerTrigger render={<Button variant='outline' />}>打开抽屉</DrawerTrigger>
      <DrawerContent className='max-h-[85dvh]'>
        <DrawerHeader><DrawerTitle>筛选记录</DrawerTitle><DrawerDescription>草稿只保存在当前示例中。</DrawerDescription></DrawerHeader>
        <div className='space-y-3 overflow-y-auto p-5'>
          <Label htmlFor='drawer-search'>搜索</Label><Input id='drawer-search' placeholder='输入关键词' />
          <Textarea aria-label='抽屉备注' placeholder='备注' />
          <label className='flex items-center gap-3 text-sm'><Checkbox defaultChecked />只显示成功请求</label>
        </div>
        <DrawerFooter><DrawerClose render={<Button />}>关闭抽屉</DrawerClose></DrawerFooter>
      </DrawerContent>
    </Drawer>
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant='secondary' />}>更多操作</DropdownMenuTrigger>
      <DropdownMenuContent><DropdownMenuItem>重命名</DropdownMenuItem><DropdownMenuItem disabled>删除</DropdownMenuItem></DropdownMenuContent>
    </DropdownMenu>
  </div>
}

export function UIFoundationPreview() {
  const { resolvedTheme, setTheme } = useTheme()
  const [checked, setChecked] = useState(true)
  return <TooltipProvider>
    <main className='bg-background text-foreground min-h-dvh px-4 py-8 sm:px-8 lg:px-12' data-testid='ui-foundation-preview'>
      <div className='mx-auto max-w-6xl'>
        <header className='mb-8 flex items-end justify-between gap-4'>
          <div><p className='text-muted-foreground mb-2 text-xs tracking-widest'>LMM / UI FOUNDATION</p><h1 className='text-2xl font-semibold tracking-tight sm:text-3xl'>更清晰，也更轻。</h1><p className='text-muted-foreground mt-2 text-sm'>Luma · Base UI 1.8 · 本地组件样式与交互预览</p></div>
          <Button variant='outline' size='icon' aria-label='切换主题' onClick={() => setTheme(resolvedTheme === 'dark' ? 'light' : 'dark')}>{resolvedTheme === 'dark' ? <Sun /> : <Moon />}</Button>
        </header>
        <div className='grid items-start gap-5 md:grid-cols-2 xl:grid-cols-3'>
          <Card><CardHeader><CardTitle>操作</CardTitle></CardHeader><CardContent className='space-y-5'>
            <div className='flex flex-wrap gap-2'><Button><Plus />创建密钥</Button><Button variant='secondary'>保存</Button><Button variant='outline'>取消</Button></div>
            <div className='flex flex-wrap items-center gap-2'><Button variant='ghost'>查看详情</Button><Button variant='destructive'>删除</Button><Button disabled>处理中</Button><Tooltip><TooltipTrigger render={<Button size='icon-sm' variant='outline' aria-label='复制' />}><Copy /></TooltipTrigger><TooltipContent>复制</TooltipContent></Tooltip></div>
            <div className='flex gap-2'><Badge>标准</Badge><Badge variant='secondary'><Check />已启用</Badge><Badge variant='outline'>草稿</Badge></div>
          </CardContent></Card>
          <Card><CardHeader><CardTitle>输入</CardTitle></CardHeader><CardContent className='space-y-4'>
            <div className='space-y-2'><Label htmlFor='preview-name'>名称</Label><Input id='preview-name' placeholder='例如：开发环境' /></div>
            <div className='space-y-2'><Label htmlFor='preview-email'>邮箱</Label><Input id='preview-email' defaultValue='格式不正确' aria-invalid='true' aria-describedby='preview-email-error' /><p id='preview-email-error' className='text-destructive text-xs'>请输入有效的邮箱地址</p></div>
            <Textarea aria-label='用途' placeholder='用途与备注' />
          </CardContent></Card>
          <Card><CardHeader><CardTitle>选项</CardTitle></CardHeader><CardContent className='space-y-6'>
            <label className='flex items-center justify-between text-sm'>自动刷新<Switch checked={checked} onCheckedChange={setChecked} /></label>
            <label className='flex items-center justify-between text-sm'>紧凑模式<Switch size='sm' /></label>
            <div className='flex flex-wrap gap-5'><label className='flex items-center gap-2 text-sm'><Checkbox defaultChecked />成功</label><label className='flex items-center gap-2 text-sm'><Checkbox />失败</label><label className='flex items-center gap-2 text-sm'><Checkbox disabled />停用</label></div>
            <RadioGroup defaultValue='standard' aria-label='请求模式' className='flex gap-5'><label className='flex items-center gap-2 text-sm'><RadioGroupItem value='standard' />标准</label><label className='flex items-center gap-2 text-sm'><RadioGroupItem value='fast' />低延迟</label></RadioGroup>
            <Slider defaultValue={[40]} aria-label='响应长度' />
          </CardContent></Card>
          <Card><CardHeader><CardTitle>切换</CardTitle></CardHeader><CardContent>
            <Tabs defaultValue='overview'><TabsList aria-label='记录类型'><TabsTrigger value='overview'>概览</TabsTrigger><TabsTrigger value='history'>历史记录</TabsTrigger><TabsTrigger value='disabled' disabled>归档</TabsTrigger></TabsList><TabsContent value='overview' keepMounted className='space-y-4 pt-5'><Input aria-label='保留的草稿' placeholder='切换后仍保留的草稿' /><p className='text-muted-foreground text-sm'>这里只展示当前需要的信息。</p></TabsContent><TabsContent value='history' keepMounted className='pt-5'><p className='text-muted-foreground text-sm'>暂无历史记录</p></TabsContent></Tabs>
          </CardContent></Card>
          <Card><CardHeader><CardTitle>安全验证</CardTitle></CardHeader><CardContent><PreviewOTP /></CardContent></Card>
          <Card><CardHeader><CardTitle>浮层</CardTitle></CardHeader><CardContent className='space-y-5'><PreviewOverlays /><p className='text-muted-foreground text-sm'>支持键盘操作与手机手势。</p></CardContent></Card>
        </div>
      </div>
    </main>
  </TooltipProvider>
}
