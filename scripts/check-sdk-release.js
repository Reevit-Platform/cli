#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const root = process.cwd();

const packages = [
  { dir: 'sdks/core', checkAssets: false },
  { dir: 'sdks/react', checkAssets: true },
  { dir: 'sdks/svelte', checkAssets: true },
  { dir: 'sdks/vue', checkAssets: true },
  { dir: 'sdks/typescript', checkAssets: false },
];

const tempFilePattern = /\.(new|tmp|bak|orig)$/;
const forbiddenDistPatterns = [
  {
    pattern: /https:\/\/sandbox-api\.reevit\.io/g,
    message: 'tracked dist contains deprecated sandbox host',
  },
  {
    pattern: /\bpk_(?:test|live|sandbox)_/g,
    message: 'tracked dist contains legacy pk_* key prefixes',
  },
  {
    pattern: /\bpfk_sandbox_/g,
    message: 'tracked dist contains deprecated pfk_sandbox_ prefix',
  },
  {
    pattern: /Authorization[^\\n]{0,120}Bearer/g,
    message: 'tracked dist contains Authorization Bearer auth',
  },
];

function repoPath(dir) {
  return path.join(root, dir);
}

function trackedFiles(dir) {
  const output = execFileSync('git', ['-C', repoPath(dir), 'ls-files', '-z'], { encoding: 'utf8' });
  return output.split('\0').filter(Boolean);
}

function collectExpectedDistFiles(pkg) {
  const expected = new Set();

  for (const key of ['main', 'module', 'types']) {
    if (typeof pkg[key] === 'string' && pkg[key].startsWith('dist/')) {
      expected.add(pkg[key]);
    }
  }

  function walkExports(value) {
    if (!value) return;
    if (typeof value === 'string' && value.startsWith('./dist/')) {
      expected.add(value.slice(2));
      return;
    }
    if (typeof value === 'object') {
      for (const child of Object.values(value)) {
        walkExports(child);
      }
    }
  }

  walkExports(pkg.exports);
  return [...expected];
}

function isPng(buffer) {
  return buffer.length >= 8 && buffer.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
}

function validatePackage(pkgInfo) {
  const dir = pkgInfo.dir;
  const absDir = repoPath(dir);
  const pkg = JSON.parse(fs.readFileSync(path.join(absDir, 'package.json'), 'utf8'));
  const tracked = trackedFiles(dir);
  const errors = [];

  for (const rel of collectExpectedDistFiles(pkg)) {
    const abs = path.join(absDir, rel);
    if (!fs.existsSync(abs)) {
      errors.push(`${dir}: missing expected build artifact ${rel}`);
    }
  }

  for (const rel of tracked) {
    const abs = path.join(absDir, rel);
    if (!fs.existsSync(abs)) {
      continue;
    }

    if (tempFilePattern.test(rel)) {
      errors.push(`${dir}: tracked temp file ${rel}`);
    }

    if (fs.statSync(abs).isFile() && fs.statSync(abs).size === 0) {
      errors.push(`${dir}: empty tracked file ${rel}`);
    }

    if (pkgInfo.checkAssets && rel.startsWith('src/assets/') && rel.endsWith('.png')) {
      const contents = fs.readFileSync(abs);
      if (!isPng(contents)) {
        errors.push(`${dir}: invalid PNG asset ${rel}`);
      }
    }

    if (rel.startsWith('dist/')) {
      const text = fs.readFileSync(abs, 'utf8');
      for (const rule of forbiddenDistPatterns) {
        if (rule.pattern.test(text)) {
          errors.push(`${dir}: ${rule.message} in ${rel}`);
        }
        rule.pattern.lastIndex = 0;
      }
    }
  }

  return errors;
}

const errors = packages.flatMap(validatePackage);

if (errors.length > 0) {
  console.error('SDK release sanity checks failed:\n');
  for (const error of errors) {
    console.error(`- ${error}`);
  }
  process.exit(1);
}

console.log('SDK release sanity checks passed.');
