// The colour schemes a person can choose in the account menu (D220). Light and dark are
// in tokens.css, the rest in schemes.css; each id is a data-theme on <html>. A swatch is
// the scheme drawn small for the menu: its page (left and right halves) and its accent.

export const themes = [
  { id: "system", label: "Same as this computer", swatch: ["#fafaf7", "#0c0d0a", "#7cdf64"] },
  { id: "light", label: "Light", swatch: ["#fafaf7", "#fafaf7", "#7cdf64"] },
  { id: "dark", label: "Dark", swatch: ["#0c0d0a", "#0c0d0a", "#14b8a6"] },
  { id: "ember", label: "Ember", hint: "warm, for evenings", swatch: ["#15110e", "#15110e", "#eab04e"] },
  { id: "tide", label: "Tide", hint: "deep sea green", swatch: ["#08191a", "#08191a", "#7cdf64"] },
  { id: "contrast", label: "High contrast", hint: "strongest text and edges", swatch: ["#050604", "#050604", "#8ff06f"] },
] as const;

export type Theme = (typeof themes)[number]["id"];

/** isTheme is true for a scheme this build knows, so a stale stored choice falls back to the system's. */
export function isTheme(id: unknown): id is Theme {
  return themes.some((t) => t.id === id);
}

/**
 * firstPaint is the script that applies the chosen scheme before the page first paints,
 * so a dark scheme never flashes light.
 */
export const firstPaint = `try{var t=JSON.parse(localStorage.getItem("aboard.theme")||"null");if(${JSON.stringify(themes.map((t) => t.id).filter((id) => id !== "system"))}.indexOf(t)>=0)document.documentElement.dataset.theme=t}catch(e){}`;
