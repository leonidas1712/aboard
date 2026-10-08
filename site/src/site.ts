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

// The independent investigation of the Hugging Face incident, published 26 August 2026.
export const INCIDENT_URL = "https://www.redwoodresearch.org/research/hugging-face-incident";
