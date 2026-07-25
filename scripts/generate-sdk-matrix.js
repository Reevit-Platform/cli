#!/usr/bin/env node
/**
 * Generate the SDK support matrix from the package registries.
 *
 * A hand-written matrix is wrong within a month — which is how the public
 * developer page came to claim a package count nobody had checked against what
 * was actually published. This queries npm, PyPI, Packagist, the Go module
 * proxy and crates.io and prints the table, so the documented state is the
 * published state by construction.
 *
 *   node scripts/generate-sdk-matrix.js            # markdown table
 *   node scripts/generate-sdk-matrix.js --json     # machine-readable
 *   node scripts/generate-sdk-matrix.js --check    # non-zero exit if any package is missing
 */

const SDKS = [
  { name: "React", registry: "npm", id: "@reevit/react", docs: "/sdks/react" },
  { name: "Vue", registry: "npm", id: "@reevit/vue", docs: "/sdks/vue" },
  { name: "Svelte", registry: "npm", id: "@reevit/svelte", docs: "/sdks/svelte" },
  { name: "Core (headless)", registry: "npm", id: "@reevit/core", docs: "/sdks/nodejs" },
  { name: "Node.js", registry: "npm", id: "@reevit/node", docs: "/sdks/nodejs" },
  { name: "CLI", registry: "npm", id: "@reevit/cli", docs: "/cli" },
  { name: "MCP server", registry: "npm", id: "@reevit/mcp", docs: "/agent-payments" },
  { name: "Python", registry: "pypi", id: "reevit", docs: "/sdks/python" },
  { name: "PHP", registry: "packagist", id: "reevit/reevit-php", docs: "/sdks/php" },
  { name: "Go", registry: "go", id: "github.com/Reevit-Platform/go-sdk", docs: "/sdks/go" },
  { name: "Rust", registry: "crates", id: "reevit", docs: "/sdks/rust" },
];

const UA = { "User-Agent": "reevit-sdk-matrix" };

async function fetchJSON(url) {
  const res = await fetch(url, { headers: UA });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

async function lookup(sdk) {
  try {
    switch (sdk.registry) {
      case "npm": {
        const d = await fetchJSON(`https://registry.npmjs.org/${sdk.id}`);
        const version = d["dist-tags"]?.latest;
        return { version, released: d.time?.[version]?.slice(0, 10) };
      }
      case "pypi": {
        const d = await fetchJSON(`https://pypi.org/pypi/${sdk.id}/json`);
        const version = d.info.version;
        const files = d.releases?.[version] ?? [];
        return { version, released: files[0]?.upload_time?.slice(0, 10) };
      }
      case "packagist": {
        const d = await fetchJSON(`https://repo.packagist.org/p2/${sdk.id}.json`);
        const latest = d.packages?.[sdk.id]?.[0];
        return { version: latest?.version, released: latest?.time?.slice(0, 10) };
      }
      case "go": {
        // The proxy serves a newline-delimited version list, not JSON.
        const res = await fetch(
          `https://proxy.golang.org/${sdk.id.toLowerCase().replace(/([A-Z])/g, "!$1")}/@v/list`,
          { headers: UA },
        );
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const versions = (await res.text()).trim().split("\n").filter(Boolean).sort();
        return { version: versions.at(-1) || "untagged", released: undefined };
      }
      case "crates": {
        const d = await fetchJSON(`https://crates.io/api/v1/crates/${sdk.id}`);
        return { version: d.crate?.max_version, released: d.crate?.updated_at?.slice(0, 10) };
      }
      default:
        throw new Error(`unknown registry ${sdk.registry}`);
    }
  } catch (err) {
    return { error: err.message };
  }
}

const REGISTRY_LABEL = {
  npm: "npm",
  pypi: "PyPI",
  packagist: "Packagist",
  go: "Go modules",
  crates: "crates.io",
};

async function main() {
  const results = await Promise.all(
    SDKS.map(async (sdk) => ({ ...sdk, ...(await lookup(sdk)) })),
  );

  if (process.argv.includes("--json")) {
    console.log(JSON.stringify(results, null, 2));
  } else {
    console.log("| SDK | Registry | Package | Version | Last release |");
    console.log("|-----|----------|---------|---------|--------------|");
    for (const r of results) {
      const version = r.error ? `**not published** (${r.error})` : `\`${r.version}\``;
      console.log(
        `| ${r.name} | ${REGISTRY_LABEL[r.registry]} | \`${r.id}\` | ${version} | ${r.released ?? "—"} |`,
      );
    }
    console.log(`\n${results.filter((r) => !r.error).length} of ${results.length} published.`);
  }

  const missing = results.filter((r) => r.error);
  if (missing.length > 0) {
    console.error(`\nUnavailable: ${missing.map((m) => m.id).join(", ")}`);
    // A package advertised but never published is the most damaging kind of
    // finding for a developer product — fail CI on it.
    if (process.argv.includes("--check")) process.exit(1);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
