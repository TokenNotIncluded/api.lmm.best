/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE ?? "playwright"
);
const origin = "http://127.0.0.1:4174";
const output = process.env.CONSOLE_REVIEW_OUTPUT;
assert.ok(output, "CONSOLE_REVIEW_OUTPUT is required");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
const report = [];
try {
  for (const width of [1440, 390]) {
    for (const colorScheme of ["light", "dark"]) {
      const context = await browser.newContext({
        viewport: { width, height: 1000 },
        colorScheme,
        locale: "zh-CN",
        serviceWorkers: "block",
      });
      await context.addCookies([
        { name: "vite-ui-theme", value: colorScheme, url: origin },
      ]);
      await context.addInitScript(() => {
        localStorage.setItem("i18nextLng", "zhCN");
        localStorage.setItem("lmm:source-consent:v2", "no");
      });
      const errors = [];
      await context.route("**/*", async (route) => {
        const url = new URL(route.request().url());
        if (url.origin !== origin) return route.abort("blockedbyclient");
        if (url.pathname === "/api/status")
          return route.fulfill({
            json: {
              success: true,
              data: {
                system_name: "LMM Best",
                assistant: { enabled: false },
                announcements_enabled: false,
              },
            },
          });
        if (url.pathname.startsWith("/api/")) {
          errors.push(`Unexpected backend request: ${url.pathname}`);
          return route.abort("blockedbyclient");
        }
        return route.continue();
      });
      const page = await context.newPage();
      page.setDefaultTimeout(15000);
      page.on("pageerror", (error) => errors.push(String(error)));
      const capture = async (name) => {
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({
          path: path.join(output, name),
          animations: "disabled",
          fullPage: true,
        });
        const dimensions = await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
        }));
        assert.ok(
          dimensions.scroll <= dimensions.width + 1,
          "document overflows viewport",
        );
        assert.deepEqual(errors, []);
        report.push({
          width,
          colorScheme,
          name,
          dimensions,
          text: await page.locator("body").innerText(),
          errors: [...errors],
        });
      };
      try {
        await page.goto(
          `${origin}/tool-market?debug_persona=l1&console_review=1`,
        );
        await page.getByTestId("persona-debug-trigger").waitFor();
        await page
          .getByRole("button", { name: "发布工具", exact: true })
          .first()
          .click();
        await page
          .locator('#market-preset option[value="monid"]')
          .waitFor({ state: "attached" });
        await page.locator("#market-preset").selectOption("monid");
        assert.equal(
          await page.locator("#market-endpoint").inputValue(),
          "https://mcp.monid.ai/v1",
        );
        assert.equal(await page.locator("#market-endpoint").isDisabled(), true);
        await page
          .locator("#market-secret")
          .fill("fixture-only-not-a-real-key");
        await page.locator("#market-multiplier").fill("1.5");
        await capture(`monid-${width}-${colorScheme}.png`);
        await page.locator("#market-preset").selectOption("agentkey");
        assert.equal(await page.locator("#market-secret").inputValue(), "");
        assert.equal(
          await page.locator("#market-endpoint").inputValue(),
          "https://api.agentkey.app/v1/mcp",
        );
        assert.equal(
          await page
            .getByRole("button", { name: "保存草稿", exact: true })
            .isDisabled(),
          true,
        );
        await capture(`agentkey-${width}-${colorScheme}.png`);
        await page.getByRole("button", { name: "取消", exact: true }).click();
        await page
          .getByRole("tab", { name: "连接与限额", exact: true })
          .click();
        await page.getByText("浏览器登录（推荐）", { exact: true }).waitFor();
        assert.ok(
          (await page.locator("pre").first().innerText()).includes(
            "/mcp/market",
          ),
        );
        await capture(`oauth-${width}-${colorScheme}.png`);
      } catch (error) {
        await page
          .screenshot({
            path: path.join(output, `failure-${width}-${colorScheme}.png`),
            fullPage: true,
          })
          .catch(() => {});
        throw error;
      } finally {
        await context.close();
      }
    }
  }
} finally {
  await writeFile(
    path.join(output, "report.json"),
    JSON.stringify(report, null, 2),
  );
  await browser.close();
}
