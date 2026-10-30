// UI smoke test: opens the frontend with the mock backend (build/devserver.js)
// in headless Chromium, walks every screen and clicks the main buttons.
// Fails on any uncaught JS error, console error or a click that does nothing.
//
//   npm install --no-save playwright@1.63.0 && npx playwright install chromium
//   node build/ui-smoke.js
'use strict';
const { chromium } = require('playwright');
const server = require('./devserver.js');

const errors = [];
const steps = [];

async function main() {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  page.setDefaultTimeout(3000);
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  page.on('dialog', d => d.accept());

  const step = async (name, fn) => {
    steps.push(name);
    try { await fn(); } catch (e) { errors.push(`${name}: ${e.message.split('\n')[0]}`); }
  };
  const view = async (v) => { await page.click(`#nav a[data-view=${v}]`); await page.waitForTimeout(250); };
  const opens = async (sel) => { await page.waitForSelector(sel, { state: 'visible', timeout: 2000 }); };
  const closeModal = async () => { await page.click('#modal-root [data-close]'); await page.waitForSelector('#modal-root .modal', { state: 'detached', timeout: 2000 }); };

  await page.goto(`http://127.0.0.1:${server.port}/`);
  await page.waitForSelector('#power', { timeout: 5000 });

  await step('home: power toggles', async () => {
    await view('home');
    await page.click('#power'); await page.waitForTimeout(300);
    await page.click('#power'); await page.waitForTimeout(300);
  });
  await step('servers: add-server dialog', async () => {
    await view('servers');
    await page.click('#add-server'); await opens('#srv-save'); await closeModal();
  });
  await step('servers: test button', async () => {
    await page.click('[data-test]'); await page.waitForTimeout(300);
  });
  await step('apps: add dialog', async () => {
    await view('apps');
    await page.click('#add-apps'); await opens('#pick-add');
    await page.click('[data-ptab=installed]'); await page.waitForTimeout(250);
    await closeModal();
  });
  await step('apps: add an app', async () => {
    const before = await page.$$eval('#rule-list .rule', r => r.length);
    await page.click('#add-apps'); await opens('#picklist .pick');
    await page.click('#picklist .pick:not(.disabled)'); await page.click('#pick-add');
    await page.waitForFunction(n => document.querySelectorAll('#rule-list .rule').length > n, before, { timeout: 2000 });
  });
  await step('apps: route select', async () => {
    await page.selectOption('[data-target]', 'direct'); await page.waitForTimeout(250);
  });
  await step('apps: "изменить" goes to settings', async () => {
    await view('apps');
    await page.click('#to-settings');
    await page.waitForFunction(() => document.querySelector('#main h1')?.textContent === 'Настройки', null, { timeout: 2000 });
  });
  await step('settings: switches and segments', async () => {
    await page.click('[data-seg=kill] button[data-v=off]'); await page.waitForTimeout(250);
    await page.click('#upd-check'); await page.waitForTimeout(250);
  });
  await step('activity', async () => { await view('activity'); await page.waitForSelector('#act table', { timeout: 3000 }); });
  await step('logs', async () => { await view('logs'); await page.click('#log-refresh'); await page.waitForTimeout(250); });

  await browser.close();
}

main()
  .catch(e => errors.push('fatal: ' + e.message))
  .finally(() => {
    server.close();
    console.log(`steps: ${steps.length}`);
    if (errors.length) {
      console.error('UI smoke test FAILED:\n  ' + errors.join('\n  '));
      process.exit(1);
    }
    console.log('UI smoke test passed');
  });
