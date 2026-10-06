// ReadProgress keeps the newest read bookkeeping (read_up_to and unread, as a pair) for
// each board, by its permanent id. Board lists, the board itself, acknowledgements and
// the stream's unread events all carry that pair, and they can arrive out of order: a
// list read before an acknowledgement can answer after the stream already said the board
// is read. A read position only moves forward; at the same position the newest source
// (by when its request started or its event arrived) says how many are unread. Board
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

  /**
   * note keeps a board's pair, seen at generation gen. A read position only moves
   * forward: a higher read_up_to is always taken and a lower one never is, whatever its
   * generation, since a read started late can still see an old position, and an event
   * can arrive late. At the same read_up_to the newer generation's unread is taken, as a
   * new message raises it without moving the position. The kept generation is the
   * newest seen.
   */
  note(id: string, gen: number, read_up_to: number | undefined, unread: number | undefined): void {
    const kept = this.pairs.get(id);
    if (!kept) {
      this.pairs.set(id, { gen, read_up_to, unread });
      return;
    }
    const was = kept.read_up_to ?? -1;
    const now = read_up_to ?? -1;
    if (now > was || (now === was && gen > kept.gen)) {
      this.pairs.set(id, { gen: Math.max(gen, kept.gen), read_up_to, unread });
    } else {
      kept.gen = Math.max(gen, kept.gen);
    }
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
