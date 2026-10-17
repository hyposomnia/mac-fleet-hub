import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const gateway = process.env.FLEET_TEST_BASE || 'http://localhost:18765';
const fixtures = [
  { name: 'file10.txt', kind: 'file', createdAt: 100, modifiedAt: 300 },
  { name: 'file2.txt', kind: 'file', createdAt: 300, modifiedAt: 200 },
  { name: 'file1.txt', kind: 'file', createdAt: 200, modifiedAt: 100 },
];
const sorts = [
  ['文件名升序', ['file1.txt', 'file2.txt', 'file10.txt']],
  ['文件名降序', ['file10.txt', 'file2.txt', 'file1.txt']],
  ['创建时间升序', ['file10.txt', 'file1.txt', 'file2.txt']],
  ['创建时间降序', ['file2.txt', 'file1.txt', 'file10.txt']],
  ['修改时间升序', ['file1.txt', 'file2.txt', 'file10.txt']],
  ['修改时间降序', ['file10.txt', 'file2.txt', 'file1.txt']],
];

try {
  for (const mobile of [false, true]) {
    const context = await browser.newContext({ viewport: mobile ? { width: 393, height: 852 } : { width: 1440, height: 900 },
      isMobile: mobile, hasTouch: mobile, deviceScaleFactor: 1 });
    const page = await context.newPage();
    const errors = [], paths = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/api/file/list*', route => {
      const path = new URL(route.request().url()).searchParams.get('path') || '/Users/fixture/project';
      paths.push(path);
      return route.fulfill({ json: { root: '/', path, entries: fixtures.map(entry => ({ ...entry, path: `${path}/${entry.name}` })) } });
    });
    await page.goto(gateway + '/?mode=files');
    await page.locator('#file-list [role="option"]').first().waitFor();
    assert.equal(await page.locator('.file-toolbar #file-path-input').count(), 0);
    assert.equal(await page.locator('.file-searchbar').evaluate(el => getComputedStyle(el).backgroundColor), 'rgba(0, 0, 0, 0)');
    const menu = page.locator('#file-settings');
    const background = await menu.evaluate(el => getComputedStyle(el).backgroundColor);
    for (const [view, title, selector] of [
      ['list', '列表视图', '#file-list > .file-row strong'],
      ['icons', '图标视图', '#file-list > .file-icon-item > strong'],
      ['columns', '分栏视图', '#file-list .file-column:last-child .file-column-row > strong'],
    ]) {
      await menu.click();
      await page.getByRole('button', { name: title, exact: true }).click();
      for (const [label, expected] of sorts) {
        await menu.click();
        await page.getByRole('button', { name: label, exact: true }).click();
        await page.waitForFunction(({ selector, expected }) =>
          [...document.querySelectorAll(selector)].map(el => el.textContent).join() === expected.join(), { selector, expected });
        assert.equal(await page.locator('#file-settings-menu').isVisible(), false);
      }
      assert.equal(await page.locator('.file-main').getAttribute('data-file-view'), view);
    }
    await menu.click();
    await page.getByRole('button', { name: '创建时间升序', exact: true }).click();
    await page.reload();
    await page.locator('#file-list [role="option"]').first().waitFor();
    await menu.click();
    assert.equal(await page.getByRole('button', { name: '创建时间升序', exact: true }).getAttribute('aria-pressed'), 'true');
    await page.screenshot({ path: `/private/tmp/fleet-file-menu-${mobile ? 'h5' : 'desktop'}.png` });
    await page.getByRole('button', { name: '前往文件夹…', exact: true }).click();
    const dialog = page.locator('#file-path-modal');
    await dialog.waitFor({ state: 'visible' });
    const box = await dialog.locator('.modal').boundingBox();
    assert.ok(box.y > 0 && box.y + box.height < page.viewportSize().height, 'path dialog is centered, not a drawer');
    await page.getByRole('textbox', { name: '文件夹路径', exact: true }).fill('relative/path');
    await dialog.getByRole('button', { name: '前往', exact: true }).click();
    assert.equal(await page.locator('#file-path-error').isVisible(), true);
    assert.equal(paths.includes('relative/path'), false);
    await page.getByRole('textbox', { name: '文件夹路径', exact: true }).fill('/Volumes/Test Folder');
    await page.screenshot({ path: `/private/tmp/fleet-file-path-${mobile ? 'h5' : 'desktop'}.png` });
    await dialog.getByRole('button', { name: '前往', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('#file-folder-title').textContent === 'Test Folder');
    assert.ok(paths.includes('/Volumes/Test Folder'));
    assert.equal(await dialog.isVisible(), false);
    if (mobile) {
      assert.equal(await page.evaluate(() => matchMedia('(hover: hover)').matches), false);
      await menu.tap();
      await page.getByRole('button', { name: '文件名升序', exact: true }).tap();
      assert.equal(await menu.evaluate(el => getComputedStyle(el).backgroundColor), background, 'touch leaves no hover background');
    }
    assert.deepEqual(errors, []);
    await context.close();
    process.stdout.write(`${mobile ? 'H5' : 'Desktop'}: all 6 sorts across 3 views, persisted selection, path dialog and touch styling passed\n`);
  }
} finally { await browser.close(); }
