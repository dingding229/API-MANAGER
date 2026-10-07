import test from 'node:test';
import assert from 'node:assert/strict';
import {formatSiteDate,setSiteTimeZone,siteDateInputToISO} from '../lib/site-time.ts';
test('site timezone is independent of browser timezone and filters match displayed time',()=>{setSiteTimeZone('Asia/Shanghai');assert.match(formatSiteDate('2026-10-07T00:00:00Z'),/08:00:00/);assert.equal(siteDateInputToISO('2026-10-07T08:00'),'2026-10-07T00:00:00.000Z');setSiteTimeZone('UTC');assert.match(formatSiteDate('2026-10-07T00:00:00Z'),/00:00:00/);assert.equal(siteDateInputToISO('2026-10-07T08:00'),'2026-10-07T08:00:00.000Z');setSiteTimeZone('Bad/Zone');assert.match(formatSiteDate('2026-10-07T00:00:00Z'),/00:00:00/);setSiteTimeZone('Asia/Shanghai')});
test('fractional-offset timezones convert correctly',()=>{setSiteTimeZone('Asia/Kathmandu');assert.equal(siteDateInputToISO('2026-10-07T08:00'),'2026-10-07T02:15:00.000Z');setSiteTimeZone('Asia/Shanghai')});
