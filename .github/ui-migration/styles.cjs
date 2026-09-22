// One-shot, source-only migration. Removed by its own preparation job.
const fs=require('fs'), path=require('path');
const ts=require(process.env.UI_TYPESCRIPT);
const root=path.resolve('apps/web');
const {twMerge}=require(path.resolve('node_modules/tailwind-merge'));
const css=fs.readFileSync(process.env.UI_LUMA_CSS,'utf8');
const styles=new Map([...css.matchAll(/\.([\w-]+)\s*\{\s*@apply\s+([^;]+);\s*\}/g)].map(m=>[m[1],m[2].replace(/\s+/g,' ').trim()]));
const aliases={buttonVariants:'button',badgeVariants:'badge',tabsListVariants:'tabs-list',toggleVariants:'toggle',buttonGroupVariants:'button-group',inputGroupAddonVariants:'input-group-addon',inputGroupButtonVariants:'input-group-button',emptyMediaVariants:'empty-media',itemVariants:'item',itemMediaVariants:'item-media',sidebarMenuButtonVariants:'sidebar-menu-button'};
function clean(s){return s.replace(/\bbg-white\b/g,'bg-background').replace(/\bring-black\/10\b/g,'ring-foreground/10').split(' ').filter(t=>!t.endsWith('!')&&!t.startsWith('!')).join(' ')}
function merged(old,recipe){let str=twMerge(old,clean(recipe));if(/\btransition-all\b/.test(str))str=str.replace(/\btransition-all\b/g,'transition-[color,background-color,border-color,box-shadow,opacity,transform]');if(/\btransition(?:-|\b)/.test(str)&&!str.includes('motion-reduce:transition-none'))str+=' motion-reduce:transition-none';if(str.includes('animate-')&&!str.includes('motion-reduce:animate-none'))str+=' motion-reduce:animate-none';return str}
function literal(n){return n&&(ts.isStringLiteral(n)||ts.isNoSubstitutionTemplateLiteral(n))}
const changed=[];
for(const file of fs.readdirSync(root+'/src/components/ui').filter(f=>f.endsWith('.tsx')&&!f.includes('.test.')&&!['drawer.tsx','input-otp.tsx'].includes(f))){
 const p=root+'/src/components/ui/'+file,src=fs.readFileSync(p,'utf8');const ast=ts.createSourceFile(p,src,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);const edits=new Map(),audit=[];
 function edit(node,style,key){if(!style||!literal(node))return;const value=merged(node.text,style);if(value!==node.text){edits.set(node.getStart(ast),{end:node.end,text:JSON.stringify(value)});audit.push(key)}}
 function attrString(node){if(!node)return;if(ts.isStringLiteral(node))return node.text;if(ts.isJsxExpression(node)&&literal(node.expression))return node.expression.text}
 function walk(n){
  if(ts.isJsxOpeningElement(n)||ts.isJsxSelfClosingElement(n)){
   const attrs=n.attributes.properties.filter(ts.isJsxAttribute),slot=attrString(attrs.find(a=>a.name.getText(ast)==='data-slot')?.initializer),cls=attrs.find(a=>a.name.getText(ast)==='className')?.initializer;
   if(slot&&styles.has('cn-'+slot)&&cls){if(ts.isJsxExpression(cls)&&ts.isCallExpression(cls.expression)&&cls.expression.expression.getText(ast)==='cn'){edit(cls.expression.arguments[0],styles.get('cn-'+slot),slot)}else if(literal(cls)){edits.set(cls.getStart(ast),{end:cls.end,text:'{'+JSON.stringify(merged(cls.text,styles.get('cn-'+slot)))+'}'});audit.push(slot)}}
  }
  if(ts.isVariableDeclaration(n)&&n.initializer&&ts.isCallExpression(n.initializer)&&n.initializer.expression.getText(ast)==='cva'){
   const key=aliases[n.name.getText(ast)];if(key){const args=n.initializer.arguments;edit(args[0],styles.get('cn-'+key),key);const obj=args[1];if(obj&&ts.isObjectLiteralExpression(obj)){const variants=obj.properties.find(x=>x.name?.getText(ast)==='variants')?.initializer;if(variants&&ts.isObjectLiteralExpression(variants))for(const variant of variants.properties){if(!ts.isPropertyAssignment(variant)||!ts.isObjectLiteralExpression(variant.initializer))continue;const v=variant.name.getText(ast).replace(/['"]/g,'');for(const opt of variant.initializer.properties){if(!ts.isPropertyAssignment(opt))continue;const o=opt.name.getText(ast).replace(/['"]/g,'');edit(opt.initializer,styles.get(`cn-${key}-${v}-${o}`),`${key}:${v}:${o}`)}}}}
  }
  ts.forEachChild(n,walk)
 }
 walk(ast);let result=src;for(const [start,e]of[...edits].sort((a,b)=>b[0]-a[0]))result=result.slice(0,start)+e.text+result.slice(e.end);
 if(result!==src){fs.writeFileSync(p,result);changed.push(file)}
}
const config=JSON.parse(fs.readFileSync(root+'/components.json','utf8'));config.style='base-luma';fs.writeFileSync(root+'/components.json',JSON.stringify(config,null,2)+'\n');
fs.writeFileSync(process.env.UI_LUMA_AUDIT,JSON.stringify(changed));
