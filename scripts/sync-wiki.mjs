#!/usr/bin/env node
/**
 * GitHub Wiki sync script (Node.js, for CI/CD)
 * Reads wiki_drafts/*.md and pushes to GitHub Wiki via REST API.
 *
 * Usage:
 *   GITHUB_TOKEN=xxx node scripts/sync-wiki.mjs [--dry-run]
 */

import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WIKI_DIR = path.resolve(__dirname, "..", "wiki_drafts");
const OWNER = process.env.GITHUB_REPOSITORY_OWNER || "wabisabi926";
const REPO = (process.env.GITHUB_REPOSITORY || "wabisabi926/faststrm").split("/")[1] || "faststrm";
const TOKEN = process.env.GITHUB_TOKEN;
const DRY_RUN = process.argv.includes("--dry-run");
const API_BASE = `https://api.github.com/repos/${OWNER}/${REPO}/wiki`;

if (!TOKEN) {
  console.error("ERROR: GITHUB_TOKEN not set");
  process.exit(1);
}

if (!fs.existsSync(WIKI_DIR)) {
  console.error(`ERROR: wiki_drafts not found: ${WIKI_DIR}`);
  process.exit(1);
}

async function githubApi(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      Accept: "application/vnd.github+json",
      "Content-Type": "application/json",
      "User-Agent": "faststrm-wiki-sync",
    },
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  return res.json();
}

async function syncFile(filePath) {
  const fileName = path.basename(filePath);
  const content = fs.readFileSync(filePath, "utf-8");
  const slug = fileName.replace(/\.md$/, "");

  console.log(`${DRY_RUN ? "[DRY RUN] " : ""}Push: ${fileName} -> [${slug}]`);

  if (DRY_RUN) return { ok: true, skipped: true };

  // Try to get existing page
  let sha = null;
  try {
    const existing = await githubApi("GET", `${API_BASE}/${encodeURIComponent(slug)}`);
    sha = existing.sha;
  } catch {
    // Page doesn't exist
  }

  const body = {
    content,
    title: slug,
    message: `docs(wiki): sync ${fileName} via sync-wiki.mjs`,
  };

  if (sha) {
    body.sha = sha;
    await githubApi("PUT", `${API_BASE}/${encodeURIComponent(slug)}`, body);
    return { ok: true, method: "PUT" };
  } else {
    await githubApi("POST", API_BASE, body);
    return { ok: true, method: "POST" };
  }
}

async function main() {
  console.log(`\n=== GitHub Wiki Sync ===`);
  console.log(`Repo: ${OWNER}/${REPO}`);
  console.log(`Dir:  ${WIKI_DIR}`);
  if (DRY_RUN) console.log(`Mode: DRY RUN\n`);
  else console.log("");

  const files = fs
    .readdirSync(WIKI_DIR)
    .filter((f) => f.endsWith(".md"))
    .sort((a, b) => a.localeCompare(b));

  let success = 0, failed = 0, skipped = 0;

  for (const file of files) {
    const filePath = path.join(WIKI_DIR, file);
    try {
      const result = await syncFile(filePath);
      if (result.skipped) skipped++;
      else {
        success++;
        console.log(`  OK (${result.method})`);
      }
    } catch (err) {
      failed++;
      console.log(`  FAIL: ${err.message}`);
    }
    // Rate limit protection
    await new Promise((r) => setTimeout(r, 300));
  }

  console.log(`\n=== Done ===`);
  console.log(`Success: ${success}  Failed: ${failed}  Skipped: ${skipped}`);
  console.log(`Wiki: https://github.com/${OWNER}/${REPO}/wiki`);
  if (DRY_RUN) console.log("Tip: remove --dry-run to actually push.");
}

main().catch((err) => {
  console.error("Fatal:", err.message);
  process.exit(1);
});
