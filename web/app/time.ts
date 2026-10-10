// The server writes times as JSON date-times whose fraction drops trailing zeros
// ("01.8Z", "01.823Z"), so comparing the strings misorders instants within a second.
/** newerFirst orders two date-time strings newest first by the instant they name. */
export function newerFirst(a: string, b: string): number {
  return Date.parse(b) - Date.parse(a);
}
