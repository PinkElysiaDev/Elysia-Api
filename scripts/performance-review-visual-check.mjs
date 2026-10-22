import { chromium } from 'playwright'
import { resolve, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { writeFile, readFile, access } from 'node:fs/promises'
import assert from 'node:assert/strict'

const root=resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const figures=JSON.parse(await readFile(join(root,'flamegraphs.json')))
const browser=await chromium.launch({headless:true})
const errors=[]
try {
  const page=await browser.newPage({viewport:{width:1500,height:1000}})
  page.on('pageerror',e=>errors.push(e.message))
  await page.goto(pathToFileURL(join(root,'index.html')).href)
  const initial=await page.locator('#rows tr').count()
  assert(initial>30,'dashboard data absent')
  await page.locator('#search').fill('image31')
  const filtered=await page.locator('#rows tr').count()
  assert(filtered>0 && filtered<initial,'scenario filter did not narrow rows')
  await page.screenshot({path:join(root,'dashboard-preview.png'),fullPage:true})
  await page.locator('#search').fill('latest')
  await page.locator('#edge').selectOption('normalized,current')
  assert(await page.locator('#rows tr').count()>0,'version edge selector has no data')
  const links=await page.locator('a[href]').evaluateAll(nodes=>nodes.map(n=>n.getAttribute('href')))
  for(const link of links) if(!/^(https?:|#)/.test(link)) await access(join(root,decodeURIComponent(link.split('?')[0])))
  await page.goto(pathToFileURL(join(root,'flamegraphs.html')).href+'?filter=cpu-image31-1-current')
  assert.equal(await page.locator('section:visible').count(),2,'expected full and protocol flamegraphs')
  await page.locator('section:visible img').first().scrollIntoViewIfNeeded()
  await page.waitForFunction(()=>[...document.querySelectorAll('section:not([hidden]) img')].every(i=>i.complete&&i.naturalWidth>0))
  await page.screenshot({path:join(root,'gallery-preview.png'),fullPage:true})
  for(const figure of figures) for(const field of ['profile','png','top']) await access(join(root,figure[field]))
  assert.deepEqual(errors,[])
  await writeFile(join(root,'visual-check.json'),JSON.stringify({status:'passed',initialRows:initial,filteredRows:filtered,figures:figures.length,links:links.length,pageErrors:errors,checks:['dashboard filters','version comparison','all dashboard local links','gallery filtering','representative PNG loading','all figure/profile/top files present']},null,2))
} finally { await browser.close() }
