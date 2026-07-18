#!/usr/bin/env node

const fs = require('fs');
const path = require('path');

const root = process.cwd();

const filesToCheck = [
  'backend/docs/developers/09-sdks.md',
  'mintlify-docs/sdks/index.mdx',
  'mintlify-docs/sdks/nodejs.mdx',
  'mintlify-docs/sdks/python.mdx',
  'mintlify-docs/sdks/go.mdx',
  'mintlify-docs/sdks/php.mdx',
  'mintlify-docs/sdks/rust.mdx',
  'sdks/core/README.md',
  'sdks/react/README.md',
  'sdks/react/preview/main.tsx',
  'sdks/svelte/README.md',
  'sdks/vue/README.md',
  'sdks/typescript/README.md',
  'sdks/go/README.md',
  'sdks/python/README.md',
  'sdks/php/README.md',
  'sdks/rust/README.md',
];

const forbiddenAnywhere = [
  {
    pattern: /https:\/\/sandbox-api\.reevit\.io/g,
    message: 'use https://api.reevit.io for both live and test Reevit keys',
  },
  {
    pattern: /localhost:8080/g,
    message: 'do not describe localhost as the default Reevit API host',
  },
];

const reevitKeyContext = /\b(publicKey|apiKey|api_key|REEVIT_API_KEY|X-Reevit-Key|new Reevit|ReevitAPIClient)\b/;
const legacyReevitKeyValue = /\b(?:pk|sk)_(?:test|live|sandbox)_/;

function read(filePath) {
  return fs.readFileSync(path.join(root, filePath), 'utf8');
}

function validateFile(filePath) {
  const content = read(filePath);
  const errors = [];
  const lines = content.split(/\r?\n/);

  lines.forEach((line, index) => {
    for (const rule of forbiddenAnywhere) {
      if (rule.pattern.test(line)) {
        errors.push(`${filePath}:${index + 1}: ${rule.message}`);
      }
      rule.pattern.lastIndex = 0;
    }

    if (reevitKeyContext.test(line) && legacyReevitKeyValue.test(line)) {
      errors.push(
        `${filePath}:${index + 1}: Reevit examples must use pfk_live_* or pfk_test_* keys`
      );
    }

    if (line.includes('Authorization') && line.includes('Bearer')) {
      errors.push(`${filePath}:${index + 1}: Reevit examples must not use Authorization: Bearer`);
    }
  });

  return errors;
}

const errors = filesToCheck
  .filter((filePath) => fs.existsSync(path.join(root, filePath)))
  .flatMap(validateFile);

if (errors.length > 0) {
  console.error('SDK example drift detected:\n');
  for (const error of errors) {
    console.error(`- ${error}`);
  }
  process.exit(1);
}

console.log('SDK example auth/key/base URL references are valid.');
