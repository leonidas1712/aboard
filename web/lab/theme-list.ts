// The lab's colour schemes beyond light and dark, defined in themes.css.

export const labThemes = [
  { id: "ember", label: "Ember (warm dark)" },
  { id: "tide", label: "Tide (sea-green dark)" },
  { id: "contrast", label: "High contrast" },
];

/** allThemes is every scheme the lab panel offers, the system's first. */
export const allThemes = [
  { id: "system", label: "Same as this computer" },
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  ...labThemes,
];

/** setTheme applies a scheme and remembers it, as the account menu does. */
export function setTheme(id: string) {
  try {
    localStorage.setItem("aboard.theme", JSON.stringify(id));
  } catch {
    // Not kept; it still applies.
  }
  if (id === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = id;
}
