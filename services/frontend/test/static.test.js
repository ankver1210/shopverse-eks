'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const pub = path.join(__dirname, '..', 'public');

test('index.html references the app bundle and stylesheet', () => {
  const html = fs.readFileSync(path.join(pub, 'index.html'), 'utf8');
  assert.match(html, /src="\/app\.js"/);
  assert.match(html, /href="\/styles\.css"/);
});

test('no inline <script> or style attributes (they would break the CSP)', () => {
  const html = fs.readFileSync(path.join(pub, 'index.html'), 'utf8');
  assert.doesNotMatch(html, /<script>(?!<\/script>)/);
  assert.doesNotMatch(html, /\sstyle="/);
  const js = fs.readFileSync(path.join(pub, 'app.js'), 'utf8');
  assert.doesNotMatch(js, /\sstyle="/);
});
