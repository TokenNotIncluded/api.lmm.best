/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Render the real production bundle with fictional, read-only API fixtures.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.HOME_REVIEW_ORIGIN ?? 'http://127.0.0.1:4175'
const output = process.env.HOME_REVIEW_OUTPUT ?? 'artifacts/home-review'
assert.ok(
  ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)
)
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const results = []
try {
  for (const [
    name,
    width,
    height,
    theme,
    motion,
    language,
    assistantEnabled,
  ] of [
    ['desktop', 1440, 1000, 'light', 'no-preference', 'zhCN'],
    ['mobile', 390, 844, 'light', 'no-preference', 'zhCN'],
    ['compact', 320, 740, 'light', 'no-preference', 'zhCN'],
    ['tablet', 768, 1024, 'light', 'no-preference', 'zhCN'],
    ['english', 390, 844, 'light', 'no-preference', 'en'],
    ['dark', 1440, 1000, 'dark', 'no-preference', 'zhCN'],
    ['reduced', 390, 844, 'light', 'reduce', 'zhCN'],
    ['desktop-reduced', 1440, 1000, 'light', 'reduce', 'en'],
    ['narrow-desktop', 900, 900, 'light', 'no-preference', 'en'],
    ['laptop', 1280, 720, 'light', 'no-preference', 'en'],
    ['landscape', 844, 390, 'light', 'no-preference', 'en'],
    ['short-phone', 390, 667, 'light', 'no-preference', 'zhCN'],
    ['french', 390, 844, 'light', 'no-preference', 'fr'],
    ['russian', 390, 844, 'light', 'no-preference', 'ru'],
    ['assistant-enabled', 390, 844, 'light', 'no-preference', 'zhCN', true],
  ].filter(
    ([name]) =>
      !process.env.HOME_REVIEW_CASES ||
      process.env.HOME_REVIEW_CASES.split(',').includes(name)
  )) {
    const context = await browser.newContext({
      viewport: { width, height },
      locale: { en: 'en-US', zhCN: 'zh-CN', fr: 'fr-FR', ru: 'ru-RU' }[
        language
      ],
      reducedMotion: motion,
      serviceWorkers: 'block',
    })
    await context.addCookies([
      { name: 'vite-ui-theme', value: theme, url: origin },
    ])
    await context.addInitScript((lang) => {
      localStorage.setItem('i18nextLng', lang)
      localStorage.setItem('lmm:source-consent:v2', 'no')
    }, language)
    await context.route('**/*', (route) => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (!url.pathname.startsWith('/api/')) return route.continue()
      if (url.pathname === '/api/user/auth/refresh') {
        return route.fulfill({ status: 401, json: { success: false } })
      }
      let data = []
      if (url.pathname === '/api/setup') data = { status: true }
      if (url.pathname === '/api/status') {
        data = {
          system_name: 'LMM',
          register_enabled: true,
          assistant: { enabled: !!assistantEnabled },
          backend_capabilities: { bounty_public_read: false },
        }
      }
      if (url.pathname === '/api/notice') data = ''
      return route.fulfill({ json: { success: true, data } })
    })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (error) => errors.push(error.message))
    try {
      await page.goto(origin, { waitUntil: 'networkidle' })
      await page.locator('#lmm-home-title').waitFor()
      await page.waitForFunction(
        () => document.querySelector('.lmm-home')?.dataset.motion !== 'loading'
      )
      await page.waitForFunction(
        () => document.querySelector('[data-film]')?.dataset.ready === 'true'
      )
      await page.evaluate(() => document.fonts.ready)
      await page.waitForTimeout(1200)
      if (language === 'en') {
        assert.match(
          await page.locator('#lmm-home-title').textContent(),
          /AI models/
        )
      }
      if (language === 'fr') {
        assert.match(
          await page.locator('#lmm-home-title').textContent(),
          /modèles IA/
        )
      }
      if (language === 'ru') {
        assert.match(
          await page.locator('#lmm-home-title').textContent(),
          /Модели ИИ/
        )
      }
      await page.screenshot({ path: `${output}/${name}.png` })
      const metrics = await page.evaluate(() => {
        const box = (selector) => {
          const rect = document.querySelector(selector)?.getBoundingClientRect()
          return (
            rect && {
              x: rect.x,
              y: rect.y,
              width: rect.width,
              height: rect.height,
            }
          )
        }
        return {
          scrollWidth: document.documentElement.scrollWidth,
          motion: document.querySelector('.lmm-home')?.dataset.motion,
          title: box('#lmm-home-title'),
          visual: box('.lmm-poster-art'),
          controls: box('.lmm-core-steps'),
          stage: box('[data-cinema-inner]'),
          runway: box('[data-cinema]'),
          activePanel: box('[data-cinema-panel][data-active]'),
          stageBorder: getComputedStyle(
            document.querySelector('[data-cinema-inner]')
          ).borderTopWidth,
          poster: (() => {
            const canvas = document.querySelector('[data-film]')
            const context = canvas.getContext('2d')
            const pixels = context?.getImageData(
              0,
              0,
              canvas.width,
              canvas.height
            ).data
            let colored = 0
            for (let index = 0; pixels && index < pixels.length; index += 64) {
              if (
                Math.max(pixels[index], pixels[index + 1], pixels[index + 2]) >
                24
              )
                colored++
            }
            return { width: canvas.width, height: canvas.height, colored }
          })(),
          buttons: Array.from(
            document.querySelectorAll('[data-cinema-jump]')
          ).map((button) => ({
            text: button.textContent,
            width: button.getBoundingClientRect().width,
            height: button.getBoundingClientRect().height,
            unobstructed: (() => {
              const rect = button.getBoundingClientRect()
              const target = document.elementFromPoint(
                rect.x + rect.width / 2,
                rect.y + rect.height / 2
              )
              return target === button || button.contains(target)
            })(),
          })),
        }
      })
      assert.equal(metrics.scrollWidth, width, `${name}: horizontal overflow`)
      assert.deepEqual(errors, [], `${name}: browser exceptions`)
      assert.equal(metrics.stageBorder, '0px', `${name}: framed stage returned`)
      assert.ok(
        metrics.poster.width > 0 && metrics.poster.height > 0,
        `${name}: poster has no bitmap`
      )
      assert.ok(metrics.poster.colored > 100, `${name}: poster canvas is blank`)
      assert.equal(
        await page.locator('[data-cinema-jump]').count(),
        5,
        `${name}: missing chapter navigation`
      )
      const cinematic =
        motion === 'no-preference' && height > 600 && width > 900
      if (cinematic) {
        assert.ok(
          metrics.runway.height >= height * 4 &&
            metrics.runway.height <= height * 4.5,
          `${name}: five chapters need their four native-scroll transitions`
        )
        assert.ok(
          metrics.controls.y + metrics.controls.height <= height,
          `${name}: navigation below the fold`
        )
        for (const button of metrics.buttons) {
          assert.ok(
            button.width >= 44 && button.height >= 44,
            `${name}: undersized navigation target`
          )
          assert.ok(
            button.unobstructed,
            `${name}: navigation covered by a widget`
          )
        }
        const toggle = page.locator('[data-motion-toggle]')
        await toggle.click()
        assert.equal(await toggle.getAttribute('aria-pressed'), 'true')
        // Let the final paused poster frame finish.
        await page.evaluate(
          () =>
            new Promise((resolve) =>
              requestAnimationFrame(() => requestAnimationFrame(resolve))
            )
        )
        const pausedPoster = await page
          .locator('[data-film]')
          .evaluate((canvas) => canvas.toDataURL())
        await page.waitForTimeout(180)
        assert.equal(
          await page
            .locator('[data-film]')
            .evaluate((canvas) => canvas.toDataURL()),
          pausedPoster,
          `${name}: paused poster keeps drawing`
        )
        await toggle.click()
        assert.equal(await toggle.getAttribute('aria-pressed'), 'false')
        await page.locator('[data-cinema-jump="1"]').press('Enter')
        await page.waitForFunction(() =>
          document
            .querySelector('[data-cinema-panel="1"]')
            ?.hasAttribute('data-active')
        )
        await page.screenshot({ path: `${output}/${name}-store.png` })
        for (const chapter of [2, 3, 4, 0]) {
          await page.locator(`[data-cinema-jump="${chapter}"]`).click()
          await page.waitForFunction(
            (index) =>
              document
                .querySelector(`[data-cinema-panel="${index}"]`)
                ?.hasAttribute('data-active'),
            chapter
          )
          await page.waitForTimeout(850)
          if (name === 'desktop') {
            await page.screenshot({
              path: `${output}/desktop-chapter-${chapter}.png`,
            })
          }
          const bounds = await page.evaluate(() => {
            const panel = document.querySelector(
              '[data-cinema-panel][data-active]'
            )
            const content = Array.from(panel.children)
              .filter((element) => getComputedStyle(element).display !== 'none')
              .map((element) => element.getBoundingClientRect())
            const controls = document
              .querySelector('.lmm-core-steps')
              .getBoundingClientRect()
            const stage = document
              .querySelector('[data-cinema-inner]')
              .getBoundingClientRect()
            return {
              top: Math.min(...content.map((rect) => rect.top)),
              bottom: Math.max(...content.map((rect) => rect.bottom)),
              controlTop: controls.top,
              stageTop: stage.top,
            }
          })
          assert.ok(
            bounds.bottom < bounds.controlTop,
            `${name}: chapter ${chapter} covers navigation`
          )
          assert.ok(
            bounds.top >= bounds.stageTop,
            `${name}: chapter ${chapter} clips above stage`
          )
        }
        for (const chapter of [1, 2, 3, 4, 0]) {
          await page.evaluate((index) => {
            const cinema = document
              .querySelector('[data-cinema]')
              .getBoundingClientRect()
            const inner = document.querySelector('[data-cinema-inner]')
            const stage = inner.getBoundingClientRect()
            const stickyTop =
              Number.parseFloat(getComputedStyle(inner).top) || 0
            window.scrollTo(
              0,
              window.scrollY +
                cinema.top -
                stickyTop +
                ((cinema.height - stage.height) * index) / 4
            )
          }, chapter)
          await page.waitForFunction(
            (index) =>
              document.querySelector('[data-cinema-inner]')?.dataset.chapter ===
              String(index),
            chapter
          )
        }
      } else {
        const panels = await page
          .locator('[data-cinema-panel]')
          .evaluateAll((elements) =>
            elements.map((panel) => {
              const style = getComputedStyle(panel)
              const rect = panel.getBoundingClientRect()
              return {
                inert: panel.inert,
                ariaHidden: panel.getAttribute('aria-hidden'),
                visible: style.visibility,
                display: style.display,
                opacity: Number(style.opacity),
                width: rect.width,
                height: rect.height,
              }
            })
          )
        assert.equal(panels.length, 5, `${name}: missing story chapters`)
        if (motion === 'reduce') {
          assert.equal(
            metrics.motion,
            'reduced',
            `${name}: reduced motion preference ignored`
          )
          assert.equal(
            await page.locator('[data-motion-toggle]').isVisible(),
            false,
            `${name}: reduced motion still offers an animation toggle`
          )
        }
        assert.ok(
          panels.every(
            (panel) =>
              !panel.inert &&
              panel.ariaHidden !== 'true' &&
              panel.visible === 'visible' &&
              panel.display !== 'none' &&
              panel.opacity > 0 &&
              panel.width > 0 &&
              panel.height > 0
          ),
          `${name}: static story must keep every chapter accessible`
        )
        const entries = page.locator(
          '[data-cinema-panel] .lmm-intro-actions a, [data-cinema-panel] .lmm-intro-actions button, [data-cinema-panel] .lmm-core-link'
        )
        assert.ok((await entries.count()) > 0, `${name}: missing story actions`)
        for (let index = 0; index < (await entries.count()); index++) {
          const entry = entries.nth(index)
          await entry.scrollIntoViewIfNeeded()
          const target = await entry.evaluate((element) => {
            const rect = element.getBoundingClientRect()
            const hit = document.elementFromPoint(
              rect.x + rect.width / 2,
              rect.y + rect.height / 2
            )
            return {
              width: rect.width,
              height: rect.height,
              unobstructed: hit === element || element.contains(hit),
            }
          })
          assert.ok(
            target.width >= 44 && target.height >= 44,
            `${name}: undersized story action`
          )
          assert.ok(
            target.unobstructed,
            `${name}: story action covered by a widget`
          )
        }
        for (const chapter of [1, 2, 3, 4]) {
          const poster = page.locator(`[data-chapter-film="${chapter}"]`)
          await poster.scrollIntoViewIfNeeded()
          await page.waitForFunction((index) => {
            const canvas = document.querySelector(
              `[data-chapter-film="${index}"]`
            )
            if (!canvas || !canvas.width || !canvas.height) return false
            const context = canvas.getContext('2d')
            const pixels = context?.getImageData(
              0,
              0,
              canvas.width,
              canvas.height
            ).data
            for (
              let offset = 0;
              pixels && offset < pixels.length;
              offset += 128
            ) {
              if (
                Math.max(
                  pixels[offset],
                  pixels[offset + 1],
                  pixels[offset + 2]
                ) > 24
              )
                return true
            }
            return false
          }, chapter)
          assert.equal(
            await page
              .locator(`[data-cinema-panel="${chapter}"]`)
              .getAttribute('aria-hidden'),
            'false',
            `${name}: chapter ${chapter} is inaccessible`
          )
        }
      }
      if (assistantEnabled) {
        const input = page.locator('#forge-home-message')
        await input.fill('Help me connect an AI app')
        const focus = await input.evaluate((element) => {
          const control = getComputedStyle(element)
          const group = getComputedStyle(element.closest('.forge-home-input'))
          return {
            inputOutline: control.outlineWidth,
            inputBorder: control.borderTopWidth,
            groupOutline: group.outlineWidth,
            groupBorder: group.borderTopWidth,
            underline: group.boxShadow,
          }
        })
        assert.equal(
          focus.inputOutline,
          '0px',
          `${name}: nested input focus box`
        )
        assert.equal(focus.inputBorder, '0px', `${name}: boxed input returned`)
        assert.equal(
          focus.groupOutline,
          '0px',
          `${name}: framed composer focus`
        )
        assert.equal(
          focus.groupBorder,
          '0px',
          `${name}: framed composer returned`
        )
        assert.notEqual(
          focus.underline,
          'none',
          `${name}: missing visible focus indicator`
        )
        assert.equal(
          await page
            .locator('.forge-home-input button[type="submit"]')
            .isDisabled(),
          false
        )
        assert.equal(
          await page.evaluate(() => document.documentElement.scrollWidth),
          width
        )
      }
      if (['desktop', 'mobile', 'dark', 'assistant-enabled'].includes(name)) {
        await page
          .locator('.lmm-assistant-section')
          .screenshot({ path: `${output}/${name}-assistant.png` })
      }
      if (['desktop', 'mobile'].includes(name)) {
        await page
          .locator('.lmm-connect')
          .screenshot({ path: `${output}/${name}-connect.png` })
        const choices = page.locator('.lmm-connection-method button')
        assert.equal(await choices.count(), 2)
        await choices.nth(1).click()
        assert.equal(await choices.nth(1).getAttribute('aria-pressed'), 'true')
        assert.equal(
          await page.evaluate(() => document.documentElement.scrollWidth),
          width
        )
        await choices.nth(0).click()
        assert.equal(await choices.nth(0).getAttribute('aria-pressed'), 'true')
      }
      assert.deepEqual(errors, [], `${name}: interaction exceptions`)
      results.push({ name, width, height, theme, language, ...metrics, errors })
    } catch (error) {
      results.push({ name, failure: String(error), errors })
      await page.screenshot({ path: `${output}/${name}-failure.png` })
    } finally {
      await context.close()
    }
  }
  await writeFile(`${output}/results.json`, JSON.stringify(results, null, 2))
  const failures = results.filter((result) => result.failure)
  console.log(JSON.stringify({ results }))
  assert.deepEqual(failures, [], 'Homepage browser review failed')
} finally {
  await browser.close()
}
