// Links the page uses in more than one place.

export const SITE_URL = "https://comeaboard.dev";

// The install line points at this site; vercel.json redirects /install to the release's
// install.sh on GitHub, so the script and its checksum and signature checks stay GitHub's.
export const INSTALL_URL = `${SITE_URL}/install`;
export const INSTALL_SCRIPT_URL = "https://github.com/leonidas1712/aboard/releases/latest/download/install.sh";
export const INSTALL_COMMAND = `curl -fsSL ${INSTALL_URL} | sh`;

// The docs are on Mintlify under their own custom domain, not served by this site.
export const DOCS_URL = "https://docs.comeaboard.dev/";
export const QUICKSTART_URL = "https://docs.comeaboard.dev/quickstart";

export const GITHUB_URL = "https://github.com/leonidas1712/aboard";
export const LICENSE_URL = `${GITHUB_URL}/blob/main/LICENSE`;
export const ROADMAP_URL = `${GITHUB_URL}/blob/main/design/ROADMAP.md`;

// Docs pages the page links to.
export const COMPARE_URL = "https://docs.comeaboard.dev/compare";
export const SAFETY_URL = "https://docs.comeaboard.dev/safety";
export const TEAM_SERVER_URL = "https://docs.comeaboard.dev/team-server";
export const COLLEAGUE_URL = "https://docs.comeaboard.dev/guides/pair-with-a-colleague";
export const AUTO_MODE_URL = "https://docs.comeaboard.dev/guides/auto-mode";

// The hosted-version waitlist is a Tally form (tally.so). Set PUBLIC_TALLY_FORM_ID in
// Vercel's environment variables (or in site/.env for a local build) to the form's id,
// the part after tally.so/r/. Without it, the page leaves the waitlist out.
// TODO(maintainer): create the Tally form and set PUBLIC_TALLY_FORM_ID.
const tallyId = String(import.meta.env.PUBLIC_TALLY_FORM_ID ?? "").trim();
export const WAITLIST_URL = /^[A-Za-z0-9]+$/.test(tallyId) ? `https://tally.so/r/${tallyId}` : "";

// The independent investigation of the Hugging Face incident, published 26 August 2026.
export const INCIDENT_URL = "https://www.redwoodresearch.org/research/hugging-face-incident";
