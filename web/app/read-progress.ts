// ReadProgress keeps the newest read bookkeeping (read_up_to and unread, as a pair) for
// each board, by its permanent id. Board lists, the board itself, acknowledgements and
// the stream's unread events all carry that pair, and they can arrive out of order: a
// list read before an acknowledgement can answer after the stream already said the board
// is read. Each source takes a generation when its request starts (or when its event
// arrives), and a pair replaces the kept one only when its generation is newer. Board
// metadata, access and lifecycle always come from the fresh response; only the pair is
// overlaid, and only for boards that response contains.

import type { Board } from "./api";

type Pair = { gen: number; read_up_to: number | undefined; unread: number | undefined };

export class ReadProgress {
  private gen = 0;
  private pairs = new Map<string, Pair>();
  private listGen = 0;

  /** next takes the generation for a request about to start, or an event that just arrived. */
  next(): number {
    return ++this.gen;
  }

  /** note keeps a board's pair, seen at generation gen, unless a newer one is kept. */
  note(id: string, gen: number, read_up_to: number | undefined, unread: number | undefined): void {
    const kept = this.pairs.get(id);
    if (!kept || gen > kept.gen) this.pairs.set(id, { gen, read_up_to, unread });
  }

  /** board returns b, read at generation gen, with the newest pair kept for it. */
  board(b: Board, gen: number): Board {
    this.note(b.id, gen, b.read_up_to, b.unread);
    const kept = this.pairs.get(b.id)!;
    return { ...b, read_up_to: kept.read_up_to, unread: kept.unread };
  }

  /**
   * list returns a board list read at generation gen with the newest pairs, or null when
   * a list whose request started later was already taken. Boards missing from it are
   * forgotten.
   */
  list(boards: Board[], gen: number): Board[] | null {
    if (gen < this.listGen) return null;
    this.listGen = gen;
    const ids = new Set(boards.map((b) => b.id));
    for (const id of this.pairs.keys()) if (!ids.has(id)) this.pairs.delete(id);
    return boards.map((b) => this.board(b, gen));
  }

  /** apply puts the newest kept pair on b, if there is one, without noting anything. */
  apply(b: Board): Board {
    const kept = this.pairs.get(b.id);
    return kept ? { ...b, read_up_to: kept.read_up_to, unread: kept.unread } : b;
  }
}
