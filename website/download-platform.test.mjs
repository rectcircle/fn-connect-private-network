import test from 'node:test';
import assert from 'node:assert/strict';
import {detectDownloadPlatform} from './download-platform.mjs';

test('recognizes desktop operating systems and prefers client hints', () => {
  assert.equal(detectDownloadPlatform({platform:'MacIntel',userAgent:'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)'}),'macos');
  assert.equal(detectDownloadPlatform({platform:'Win32'}),'windows');
  assert.equal(detectDownloadPlatform({platform:'Linux aarch64'}),'linux');
  assert.equal(detectDownloadPlatform({userAgentData:{platform:'Windows'},platform:''}),'windows');
  assert.equal(detectDownloadPlatform({userAgent:'Mozilla/5.0 (X11; Linux x86_64)'}),'linux');
});
test('mobile devices select their own installer; unknown systems do not select a desktop installer', () => {
  assert.equal(detectDownloadPlatform({userAgentData:{platform:'Android'},userAgent:'Linux; Android 15'}),'android');
  assert.equal(detectDownloadPlatform({platform:'iPhone'}),'ios');
  assert.equal(detectDownloadPlatform({platform:'MacIntel',maxTouchPoints:5}),'ios');
  assert.equal(detectDownloadPlatform(),null);
});
test('HarmonyOS markers take precedence over Android compatibility tokens', () => {
  assert.equal(detectDownloadPlatform({userAgent:'Mozilla/5.0 (Linux; Android; HarmonyOS 6.0)'}),'harmonyos');
  assert.equal(detectDownloadPlatform({userAgentData:{platform:'OpenHarmony'}}),'harmonyos');
  assert.equal(detectDownloadPlatform({userAgent:'Linux; Android 15'}),'android');
});
