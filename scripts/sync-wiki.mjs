#!/usr/bin/env node
/**
 * GitHub Wiki sync script (Node.js, for CI/CD)
 * Clones the wiki.git repo, copies wiki_drafts/*.md, commits & pushes.
 *
 * Usage:
 *   GITHUB_TOKEN=xxx node scripts/sync-wiki.mjs [--dry-run]
 */

import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { execSync } from "child_process";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WIKI_DRAFTS_DIR = path.resolve(__dirname, "..", "wiki_drafts");
const OWNER = process.env.GITHUB_REPOSITORY_OWNER || "wabisabi926";
const REPO = (process.env.GITHUB_REPOSITORY || "wabisabi926/faststrm").split("/")[1] || "faststrm";
const TOKEN = process.env.GITHUB_TOKEN;
const DRY_RUN = process.argv.includes("--dry-run");
const GIT_USER_NAME = process.env.GIT_USER_NAME || "github-actions[bot]";
const GIT_USER_EMAIL = process.env.GIT_USER_EMAIL || "41898282+github-actions[bot]@users.noreply.github.com";

const WIKI_GIT_URL = TOKEN
  ? `https://x-access-token:${TOKEN}@github.com/${OWNER}/${REPO}.wiki.git`
  : `https://github.com/${OWNER}/${REPO}.wiki.git`;

const WORK_DIR = path.resolve(__dirname, "..", ".wiki-sync-tmp");

function run(cmd, opts = {}) {
  console.log(`  $ ${cmd}`);
  return execSync(cmd, { stdio: DRY_RUN ? "inherit" : "pipe", ...opts }).toString().trim();
}

if (!TOKEN && !DRY_RUN) {
  console.error("ERROR: GITHUB_TOKEN not set (need 'repo' scope for wiki push)");
  process.exit(1);
}

if (!fs.existsSync(WIKI_DRAFTS_DIR)) {
  console.error(`ERROR: wiki_drafts not found: ${WIKI_DRAFTS_DIR}`);
  process.exit(1);
}

async function main() {
  console.log(`\n=== GitHub Wiki Sync (Git Mode) ===`);
  console.log(`Repo:     ${OWNER}/${REPO}`);
  console.log(`Wiki URL: https://github.com/${OWNER}/${REPO}/wiki`);
  console.log(`Drafts:   ${WIKI_DRAFTS_DIR}`);
  if (DRY_RUN) console.log(`Mode:     DRY RUN\n`);
  else console.log("");

  // Clean & clone wiki repo
  console.log("> Step 1: Clone wiki repo...");
  if (fs.existsSync(WORK_DIR)) {
    fs.rmSync(WORK_DIR, { recursive: true, force: true });
  }
  if (DRY_RUN) {
    console.log(`  [DRY RUN] git clone ${WIKI_GIT_URL.replace(TOKEN || "TOKEN", "***")} ${WORK_DIR}`);
    fs.mkdirSync(WORK_DIR, { recursive: true });
  } else {
    run(`git clone --depth=1 "${WIKI_GIT_URL}" "${WORK_DIR}"`);
  }

  // Configure git identity in the cloned repo
  console.log("\n> Step 2: Configure git identity...");
  if (DRY_RUN) {
    console.log(`  [DRY RUN] git config user.name "${GIT_USER_NAME}"`);
    console.log(`  [DRY RUN] git config user.email "${GIT_USER_EMAIL}"`);
  } else {
    run(`git -C "${WORK_DIR}" config user.name "${GIT_USER_NAME}"`);
    run(`git -C "${WORK_DIR}" config user.email "${GIT_USER_EMAIL}"`);
  }

  // Sync files: remove existing .md, copy new ones
  console.log("\n> Step 3: Sync wiki_drafts/*.md into cloned wiki...");
  const existingMdFiles = DRY_RUN
    ? []
    : fs
        .readdirSync(WORK_DIR)
        .filter((f) => f.endsWith(".md"));

  for (const f of existingMdFiles) {
    const fp = path.join(WORK_DIR, f);
    if (DRY_RUN) console.log(`  [DRY RUN] rm ${fp}`);
    else {
      fs.rmSync(fp, { force: true });
      console.log(`  removed ${f}`);
    }
  }

  const draftFiles = fs
    .readdirSync(WIKI_DRAFTS_DIR)
    .filter((f) => f.endsWith(".md"))
    .sort((a, b) => a.localeCompare(b));

  for (const f of draftFiles) {
    const src = path.join(WIKI_DRAFTS_DIR, f);
    const dst = path.join(WORK_DIR, f);
    if (DRY_RUN) console.log(`  [DRY RUN] cp ${src} -> ${dst}`);
    else {
      fs.copyFileSync(src, dst);
      console.log(`  copied  ${f}`);
    }
  }

  // Commit & push
  console.log("\n> Step 4: Commit & push...");
  let statusOutput = "";
  try {
    statusOutput = DRY_RUN ? "?? Home.md" : run(`git -C "${WORK_DIR}" status --porcelain`);
  } catch (e) {
    statusOutput = "";
  }

  if (!statusOutput.trim()) {
    console.log("  No changes detected, skip commit & push.");
  } else if (DRY_RUN) {
    console.log("  [DRY RUN] git add -A");
    console.log("  [DRY RUN] git commit -m \"docs(wiki): sync pages via sync-wiki.mjs\"");
    console.log("  [DRY RUN] git push origin HEAD");
  } else {
    run(`git -C "${WORK_DIR}" add -A`);
    run(`git -C "${WORK_DIR}" commit -m "docs(wiki): sync pages via sync-wiki.mjs"`);
    run(`git -C "${WORK_DIR}" push origin HEAD`);
    console.log("  Push success ✅");
  }

  // Cleanup
  console.log("\n> Step 5: Cleanup working dir...");
  if (fs.existsSync(WORK_DIR) && !DRY_RUN) {
    fs.rmSync(WORK_DIR, { recursive: true, force: true });
    console.log("  Done.");
  }

  console.log(`\n=== Done ===`);
  console.log(`Wiki: https://github.com/${OWNER}/${REPO}/wiki`);
  if (DRY_RUN) console.log("Tip: remove --dry-run to actually push.");
}

main().catch((err) => {
  console.error("Fatal:", err.message);
  if (fs.existsSync(WORK_DIR) && !DRY_RUN) {
    try { fs.rmSync(WORK_DIR, { recursive: true, force: true }); } catch {}
  }
  process.exit(1);
});
