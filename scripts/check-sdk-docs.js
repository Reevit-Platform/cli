#!/usr/bin/env node

const fs = require('fs');
const path = require('path');

const root = process.cwd();

function resolve(...segments) {
  return path.join(root, ...segments);
}

function exists(filePath) {
  return fs.existsSync(resolve(filePath));
}

function read(filePath) {
  return fs.readFileSync(resolve(filePath), 'utf8');
}

function collectMethodsFromFile(filePath, pattern, exclude = new Set()) {
  const content = read(filePath);
  const methods = new Set();
  for (const match of content.matchAll(pattern)) {
    const method = match[1];
    if (!exclude.has(method)) {
      methods.add(method);
    }
  }
  return methods;
}

function collectTypeScriptSurface() {
  const indexPath = 'sdks/typescript/src/index.ts';
  const content = read(indexPath);
  const classToFile = new Map();
  const surface = new Map();

  for (const match of content.matchAll(/import \{ (\w+) \} from '\.\/services\/([^']+)';/g)) {
    classToFile.set(match[1], `sdks/typescript/src/services/${match[2]}.ts`);
  }

  for (const match of content.matchAll(/public (\w+): (\w+);/g)) {
    const serviceName = match[1];
    const className = match[2];
    const filePath = classToFile.get(className);
    if (!filePath || !exists(filePath)) {
      continue;
    }

    surface.set(
      serviceName,
      collectMethodsFromFile(filePath, /async (\w+)\(/g, new Set(['constructor']))
    );
  }

  return surface;
}

function collectPythonSurface() {
  const clientPath = 'sdks/python/reevit/client.py';
  const content = read(clientPath);
  const classToFile = new Map();
  const surface = new Map();

  for (const match of content.matchAll(/from \.services\.(\w+) import (\w+)Service/g)) {
    classToFile.set(`${match[2]}Service`, `sdks/python/reevit/services/${match[1]}.py`);
  }

  for (const match of content.matchAll(/self\.(\w+) = (\w+)Service\(self\)/g)) {
    const serviceName = match[1];
    const className = `${match[2]}Service`;
    const filePath = classToFile.get(className);
    if (!filePath || !exists(filePath)) {
      continue;
    }

    surface.set(
      serviceName,
      collectMethodsFromFile(filePath, /^\s+def (\w+)\(/gm, new Set(['__init__']))
    );
  }

  return surface;
}

function collectGoSurface() {
  const clientPath = 'sdks/go/client.go';
  const content = read(clientPath);
  const methodsByClass = new Map();
  const surface = new Map();
  const goDir = resolve('sdks/go');

  for (const entry of fs.readdirSync(goDir)) {
    if (!entry.endsWith('.go')) {
      continue;
    }
    const filePath = path.join(goDir, entry);
    const fileContent = fs.readFileSync(filePath, 'utf8');
    for (const match of fileContent.matchAll(/func \(s \*(\w+Service)\) (\w+)\(/g)) {
      const className = match[1];
      const methodName = match[2];
      if (!methodsByClass.has(className)) {
        methodsByClass.set(className, new Set());
      }
      methodsByClass.get(className).add(methodName);
    }
  }

  for (const match of content.matchAll(/^\s+(\w+)\s+\*(\w+Service)\s*$/gm)) {
    const serviceName = match[1];
    const className = match[2];
    if (methodsByClass.has(className)) {
      surface.set(serviceName, methodsByClass.get(className));
    }
  }

  return surface;
}

function collectPHPSurface() {
  const clientPath = 'sdks/php/src/Reevit.php';
  const content = read(clientPath);
  const surface = new Map();

  for (const match of content.matchAll(/public (\w+Service) \$(\w+);/g)) {
    const className = match[1];
    const serviceName = match[2];
    const filePath = `sdks/php/src/Services/${className}.php`;
    if (!exists(filePath)) {
      continue;
    }

    surface.set(
      serviceName,
      collectMethodsFromFile(filePath, /public function (\w+)\(/g, new Set(['__construct']))
    );
  }

  return surface;
}

function collectRustSurface() {
  const resourcesDir = resolve('sdks/rust/src/resources');
  const surface = new Map();

  if (!fs.existsSync(resourcesDir)) {
    return surface;
  }

  for (const entry of fs.readdirSync(resourcesDir)) {
    if (!entry.endsWith('.rs') || entry === 'mod.rs') {
      continue;
    }
    const content = fs.readFileSync(path.join(resourcesDir, entry), 'utf8');
    const methods = new Set();
    for (const match of content.matchAll(/pub async fn (\w+)\s*\(/g)) {
      methods.add(match[1]);
    }
    surface.set(entry.replace(/\.rs$/, ''), methods);
  }

  return surface;
}

function validateDocReferences(language, surface, files, pattern) {
  const errors = [];

  for (const filePath of files) {
    if (!exists(filePath)) {
      continue;
    }

    const content = read(filePath);
    for (const match of content.matchAll(pattern)) {
      const serviceName = match[1];
      const methodName = match[2];
      const methods = surface.get(serviceName);
      if (!methods) {
        errors.push(`${filePath}: unknown service "${serviceName}"`);
        continue;
      }
      if (!methods.has(methodName)) {
        errors.push(`${filePath}: unknown method "${serviceName}.${methodName}"`);
      }
    }
  }

  return errors.map((error) => `${language}: ${error}`);
}

const surfaces = {
  node: collectTypeScriptSurface(),
  python: collectPythonSurface(),
  go: collectGoSurface(),
  php: collectPHPSurface(),
  rust: collectRustSurface(),
};

const errors = [
  ...validateDocReferences(
    'node',
    surfaces.node,
    ['mintlify-docs/sdks/nodejs.mdx', 'sdks/typescript/README.md'],
    /\b(?:reevit|client)\.(\w+)\.(\w+)\(/g
  ),
  ...validateDocReferences(
    'python',
    surfaces.python,
    ['mintlify-docs/sdks/python.mdx', 'sdks/python/README.md'],
    /\bclient\.(\w+)\.(\w+)\(/g
  ),
  ...validateDocReferences(
    'go',
    surfaces.go,
    ['mintlify-docs/sdks/go.mdx', 'sdks/go/README.md'],
    /\bclient\.(\w+)\.(\w+)\(/g
  ),
  ...validateDocReferences(
    'php',
    surfaces.php,
    ['mintlify-docs/sdks/php.mdx', 'sdks/php/README.md'],
    /\$client->(\w+)->(\w+)\(/g
  ),
  ...validateDocReferences(
    'rust',
    surfaces.rust,
    ['mintlify-docs/sdks/rust.mdx', 'sdks/rust/README.md'],
    /\bclient\s*\.\s*(\w+)\(\)\s*\.\s*(\w+)\(/g
  ),
];

if (errors.length > 0) {
  console.error('SDK docs drift detected:\n');
  for (const error of errors) {
    console.error(`- ${error}`);
  }
  process.exit(1);
}

console.log('SDK docs references match exported SDK services.');
