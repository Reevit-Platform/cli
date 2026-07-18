#!/usr/bin/env node

/**
 * Migration Script: Fumadocs to Mintlify
 *
 * This script transforms MDX files from Fumadocs format to Mintlify format.
 * Run with: node scripts/migrate-to-mintlify.js
 */

const fs = require("fs");
const path = require("path");

// Configuration
const SOURCE_DIR = path.join(__dirname, "../docs/content/docs/reevit");
const TARGET_DIR = path.join(__dirname, "../mintlify-docs");

// Icon mapping: Hugeicons → Lucide
const ICON_MAP = {
  CreditCardIcon: "credit-card",
  Link01Icon: "link",
  LinkIcon: "link",
  RepeatIcon: "repeat",
  GlobeIcon: "globe",
  WebhookIcon: "globe",
  WorkflowSquare01Icon: "workflow",
  WorkflowIcon: "workflow",
  Shield01Icon: "shield-check",
  ShieldCheckIcon: "shield-check",
  CodeIcon: "code",
  LabsIcon: "flask-conical",
  FlaskConicalIcon: "flask-conical",
  Route01Icon: "route",
  RouteIcon: "route",
  AlertCircleIcon: "alert-triangle",
  AlertTriangleIcon: "alert-triangle",
  BookOpen01Icon: "book-open",
  BookOpenIcon: "book-open",
  PlayIcon: "play",
  Key01Icon: "key",
  KeyIcon: "key",
  // Frontmatter icon names
  Book: "book",
  Play: "play",
  CreditCard: "credit-card",
  Link: "link",
  Repeat: "repeat",
  Globe: "globe",
  Workflow: "workflow",
  Shield: "shield-check",
  Code: "code",
  Labs: "flask-conical",
  Route: "route",
  AlertCircle: "alert-triangle",
  BookOpen: "book-open",
  Key: "key",
};

// Custom diagram components to replace with images
const DIAGRAM_COMPONENTS = [
  "IntroDiagram",
  "ShopDiagram",
  "ChannelDiagram",
  "LinkDiagram",
  "MatchDiagram",
  "DocShowcase",
  "ApiExplorer",
];

// Files to migrate (excluding openapi auto-generated files)
const MAIN_PAGES = [
  "index.mdx",
  "what-is-reevit.mdx",
  "getting-started.mdx",
  "security.mdx",
  "test-mode.mdx",
  "payments.mdx",
  "connections.mdx",
  "customers.mdx",
  "payment-links.mdx",
  "checkout.mdx",
  "subscriptions.mdx",
  "webhooks.mdx",
  "workflows.mdx",
  "routing-rules.mdx",
  "ab-testing.mdx",
  "fraud-policies.mdx",
  "authentication.mdx",
  "error-codes.mdx",
  "api-reference.mdx",
];

const SDK_PAGES = [
  "sdks/index.mdx",
  "sdks/react.mdx",
  "sdks/vue.mdx",
  "sdks/svelte.mdx",
  "sdks/nodejs.mdx",
  "sdks/python.mdx",
  "sdks/go.mdx",
  "sdks/php.mdx",
];

/**
 * Transform a single MDX file from Fumadocs to Mintlify format
 */
function transformMdx(content, filename) {
  let result = content;

  // 1. Remove all import statements ONLY outside of code blocks
  // First, protect code blocks by replacing them temporarily
  const codeBlocks = [];
  result = result.replace(/```[\s\S]*?```/g, (match) => {
    codeBlocks.push(match);
    return `__CODE_BLOCK_${codeBlocks.length - 1}__`;
  });

  // Now remove imports (they're only outside code blocks now)
  result = result.replace(
    /^import\s+\{[\s\S]*?\}\s+from\s+['"].*?['"];?\s*$/gm,
    ""
  );
  result = result.replace(/^import\s+.*?from\s+['"].*?['"];?\s*$/gm, "");

  // Restore code blocks
  result = result.replace(/__CODE_BLOCK_(\d+)__/g, (match, index) => {
    return codeBlocks[parseInt(index)];
  });

  // 2. Transform frontmatter icons
  result = result.replace(
    /^(---\s*\n[\s\S]*?icon:\s*)(\w+)(\s*\n[\s\S]*?---)/m,
    (match, before, iconName, after) => {
      const mappedIcon = ICON_MAP[iconName] || iconName.toLowerCase();
      return `${before}${mappedIcon}${after}`;
    }
  );

  // 3. Replace <Cards> (with or without attributes) with <CardGroup cols={2}>
  result = result.replace(/<Cards(\s+[^>]*)?>|<Cards>/g, "<CardGroup cols={2}>");
  result = result.replace(/<\/Cards>/g, "</CardGroup>");

  // 4. Transform Card components with HugeiconsIcon
  // Pattern: <Card ... icon={<HugeiconsIcon icon={Z} />} ...>
  result = result.replace(
    /<Card\s+([\s\S]*?)icon=\{<HugeiconsIcon\s+icon=\{(\w+)\}\s*\/>\}([\s\S]*?)>/g,
    (match, before, iconVar, after) => {
      const mappedIcon =
        ICON_MAP[iconVar] || iconVar.toLowerCase().replace(/icon$/i, "");
      return `<Card ${before.trim()} icon="${mappedIcon}"${after}>`;
    }
  );

  // 5. Update internal links from /docs/reevit/X to /X
  result = result.replace(/href="\/docs\/reevit\//g, 'href="/');
  result = result.replace(/\]\(\/docs\/reevit\//g, "](/");

  // 6. Replace custom diagram components with placeholder images
  for (const component of DIAGRAM_COMPONENTS) {
    const regex = new RegExp(`<${component}\\s*\\/?>`, "g");
    result = result.replace(
      regex,
      `{/* TODO: Replace ${component} with static image */}`
    );
  }

  // 7. Transform Callout to Mintlify equivalents
  result = result.replace(/<Callout\s+type="warning">/g, "<Warning>");
  result = result.replace(/<Callout\s+type="error">/g, "<Warning>");
  result = result.replace(/<Callout\s+type="info">/g, "<Info>");
  result = result.replace(/<Callout\s+type="tip">/g, "<Tip>");
  result = result.replace(/<Callout>/g, "<Note>");

  // Fix Callout closing tags - need to match pairs
  // This is a simplified approach - may need manual review
  result = result.replace(
    /<Warning>([\s\S]*?)<\/Callout>/g,
    "<Warning>$1</Warning>"
  );
  result = result.replace(/<Info>([\s\S]*?)<\/Callout>/g, "<Info>$1</Info>");
  result = result.replace(/<Tip>([\s\S]*?)<\/Callout>/g, "<Tip>$1</Tip>");
  result = result.replace(/<Note>([\s\S]*?)<\/Callout>/g, "<Note>$1</Note>");

  // 8. Clean up multiple consecutive empty lines
  result = result.replace(/\n{3,}/g, "\n\n");

  // 9. Remove any remaining HugeiconsIcon references
  result = result.replace(/<HugeiconsIcon[^>]*\/>/g, "");

  return result.trim() + "\n";
}

/**
 * Get the target filename (handle index.mdx → introduction.mdx)
 */
function getTargetFilename(sourceFile) {
  if (sourceFile === "index.mdx") {
    return "introduction.mdx";
  }
  return sourceFile;
}

/**
 * Migrate a single file
 */
function migrateFile(sourceFile) {
  const sourcePath = path.join(SOURCE_DIR, sourceFile);
  const targetFile = getTargetFilename(sourceFile);
  const targetPath = path.join(TARGET_DIR, targetFile);

  // Ensure target directory exists
  const targetDir = path.dirname(targetPath);
  if (!fs.existsSync(targetDir)) {
    fs.mkdirSync(targetDir, { recursive: true });
  }

  // Read source file
  if (!fs.existsSync(sourcePath)) {
    console.log(`⚠️  Skipping (not found): ${sourceFile}`);
    return;
  }

  const content = fs.readFileSync(sourcePath, "utf-8");

  // Transform content
  const transformed = transformMdx(content, sourceFile);

  // Write to target
  fs.writeFileSync(targetPath, transformed);

  console.log(`✅ Migrated: ${sourceFile} → ${targetFile}`);
}

/**
 * Main migration function
 */
function main() {
  console.log("🚀 Starting Fumadocs → Mintlify migration...\n");

  // Ensure target directories exist
  if (!fs.existsSync(TARGET_DIR)) {
    fs.mkdirSync(TARGET_DIR, { recursive: true });
  }

  const sdksDir = path.join(TARGET_DIR, "sdks");
  if (!fs.existsSync(sdksDir)) {
    fs.mkdirSync(sdksDir, { recursive: true });
  }

  // Migrate main pages
  console.log("📄 Migrating main pages...");
  for (const file of MAIN_PAGES) {
    try {
      migrateFile(file);
    } catch (error) {
      console.error(`❌ Error migrating ${file}:`, error.message);
    }
  }

  // Migrate SDK pages
  console.log("\n📦 Migrating SDK pages...");
  for (const file of SDK_PAGES) {
    try {
      migrateFile(file);
    } catch (error) {
      console.error(`❌ Error migrating ${file}:`, error.message);
    }
  }

  console.log("\n✨ Migration complete!");
  console.log("\nNext steps:");
  console.log("1. Review migrated files in mintlify-docs/");
  console.log(
    "2. Create static diagram images in mintlify-docs/images/diagrams/"
  );
  console.log("3. Run 'npx mintlify dev' to preview");
  console.log("4. Fix any remaining component issues manually");
}

// Run migration
main();
