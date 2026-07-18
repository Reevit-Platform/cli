#!/usr/bin/env npx ts-node

/**
 * Migration Script: Fumadocs to Mintlify
 *
 * This script transforms MDX files from Fumadocs format to Mintlify format.
 * Run with: npx ts-node scripts/migrate-to-mintlify.ts
 */

import * as fs from "fs";
import * as path from "path";

// Configuration
const SOURCE_DIR = path.join(__dirname, "../docs/content/docs/reevit");
const TARGET_DIR = path.join(__dirname, "../mintlify-docs");

// Icon mapping: Hugeicons → Lucide
const ICON_MAP: Record<string, string> = {
  CreditCardIcon: "credit-card",
  "Link01Icon": "link",
  LinkIcon: "link",
  RepeatIcon: "repeat",
  GlobeIcon: "globe",
  WebhookIcon: "globe",
  "WorkflowSquare01Icon": "workflow",
  WorkflowIcon: "workflow",
  "Shield01Icon": "shield-check",
  ShieldCheckIcon: "shield-check",
  CodeIcon: "code",
  LabsIcon: "flask-conical",
  FlaskConicalIcon: "flask-conical",
  "Route01Icon": "route",
  RouteIcon: "route",
  AlertCircleIcon: "alert-triangle",
  AlertTriangleIcon: "alert-triangle",
  "BookOpen01Icon": "book-open",
  BookOpenIcon: "book-open",
  PlayIcon: "play",
  "Key01Icon": "key",
  KeyIcon: "key",
  Book: "book",
  Play: "play",
  // Frontmatter icon names
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
function transformMdx(content: string, filename: string): string {
  let result = content;

  // 1. Remove all import statements
  result = result.replace(
    /^import\s+.*?from\s+['"].*?['"];?\s*$/gm,
    ""
  );
  result = result.replace(
    /^import\s+{[\s\S]*?}\s+from\s+['"].*?['"];?\s*$/gm,
    ""
  );

  // 2. Transform frontmatter icons
  result = result.replace(
    /^(---\s*\n[\s\S]*?icon:\s*)(\w+)(\s*\n[\s\S]*?---)/m,
    (match, before, iconName, after) => {
      const mappedIcon = ICON_MAP[iconName] || iconName.toLowerCase();
      return `${before}${mappedIcon}${after}`;
    }
  );

  // 3. Replace <Cards> with <CardGroup cols={2}>
  result = result.replace(/<Cards>/g, "<CardGroup cols={2}>");
  result = result.replace(/<\/Cards>/g, "</CardGroup>");

  // 4. Transform Card components with HugeiconsIcon
  // Pattern: <Card title="X" href="Y" icon={<HugeiconsIcon icon={Z} />}>
  result = result.replace(
    /<Card\s+([\s\S]*?)icon=\{<HugeiconsIcon\s+icon=\{(\w+)\}\s*\/>\}([\s\S]*?)>/g,
    (match, before, iconVar, after) => {
      const mappedIcon = ICON_MAP[iconVar] || iconVar.toLowerCase().replace(/icon$/i, "");
      return `<Card ${before}icon="${mappedIcon}"${after}>`;
    }
  );

  // 5. Update internal links from /docs/reevit/X to /X
  result = result.replace(/href="\/docs\/reevit\//g, 'href="/');

  // 6. Replace custom diagram components with placeholder images
  for (const component of DIAGRAM_COMPONENTS) {
    const regex = new RegExp(`<${component}\\s*\\/?>`, "g");
    result = result.replace(
      regex,
      `{/* TODO: Replace with static image */}\n![${component}](/images/diagrams/${component.toLowerCase()}.svg)`
    );
  }

  // 7. Transform Callout to Mintlify equivalents
  result = result.replace(/<Callout\s+type="warning">/g, "<Warning>");
  result = result.replace(/<Callout\s+type="error">/g, "<Warning>");
  result = result.replace(/<Callout\s+type="info">/g, "<Info>");
  result = result.replace(/<Callout\s+type="tip">/g, "<Tip>");
  result = result.replace(/<Callout>/g, "<Note>");
  result = result.replace(/<\/Callout>/g, (match) => {
    // Need to figure out which closing tag to use based on context
    // For simplicity, we'll handle this in a second pass
    return "</Note>";
  });

  // Fix Callout closing tags based on the opening tag
  result = result.replace(/<Warning>([\s\S]*?)<\/Note>/g, "<Warning>$1</Warning>");
  result = result.replace(/<Info>([\s\S]*?)<\/Note>/g, "<Info>$1</Info>");
  result = result.replace(/<Tip>([\s\S]*?)<\/Note>/g, "<Tip>$1</Tip>");

  // 8. Clean up multiple consecutive empty lines
  result = result.replace(/\n{3,}/g, "\n\n");

  // 9. Remove any remaining HugeiconsIcon references
  result = result.replace(/<HugeiconsIcon[^>]*\/>/g, "");

  // 10. Rename index.mdx to introduction.mdx content adjustment
  if (filename === "index.mdx") {
    // Update title in frontmatter if needed
    result = result.replace(
      /^(---\s*\ntitle:\s*)Introduction/m,
      "$1Introduction"
    );
  }

  return result.trim() + "\n";
}

/**
 * Get the target filename (handle index.mdx → introduction.mdx)
 */
function getTargetFilename(sourceFile: string): string {
  if (sourceFile === "index.mdx") {
    return "introduction.mdx";
  }
  return sourceFile;
}

/**
 * Migrate a single file
 */
function migrateFile(sourceFile: string): void {
  const sourcePath = path.join(SOURCE_DIR, sourceFile);
  const targetFile = getTargetFilename(sourceFile);
  const targetPath = path.join(TARGET_DIR, targetFile);

  // Ensure target directory exists
  const targetDir = path.dirname(targetPath);
  if (!fs.existsSync(targetDir)) {
    fs.mkdirSync(targetDir, { recursive: true });
  }

  // Read source file
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
function main(): void {
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
      console.error(`❌ Error migrating ${file}:`, error);
    }
  }

  // Migrate SDK pages
  console.log("\n📦 Migrating SDK pages...");
  for (const file of SDK_PAGES) {
    try {
      migrateFile(file);
    } catch (error) {
      console.error(`❌ Error migrating ${file}:`, error);
    }
  }

  console.log("\n✨ Migration complete!");
  console.log("\nNext steps:");
  console.log("1. Review migrated files in mintlify-docs/");
  console.log("2. Create static diagram images in mintlify-docs/images/diagrams/");
  console.log("3. Run 'npx mintlify dev' to preview");
  console.log("4. Fix any remaining component issues manually");
}

// Run migration
main();
