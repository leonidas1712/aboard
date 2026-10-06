// ReadProgress keeps the newest read bookkeeping (read_up_to and unread, as a pair) for
// each board, by its permanent id. Board lists, the board itself, acknowledgements and
// the stream's unread events all carry that pair, and they can arrive out of order: a
// list read before an acknowledgement can answer after the stream already said the board
// is read. A read position only moves forward; at the same position the newest source
// (by when its request started or its event arrived) says how many are unread. Board
// metadata, access and lifecycle always come from the fresh response; only the pair is
// overlaid, and only for boards that response contains.
//
// Known limit: a stream event sent before the person left a board that arrives only
// after they rejoined it looks the same as a new one, since neither carries anything
// that says which membership it belongs to. Telling them apart needs an epoch from the
// server, so such an event can still set the count until the next fresh read.

import type { Board } from "./api";

// A pair is off when the newest fresh read said the person isn't on the board: it holds
// no bookkeeping, and only a fresh read that started later, with the person on the
// board again, starts it afresh. member is the generation of the newest fresh read
// taken, whether it found the person on the board or not; an older fresh read never
// changes whether they are on it, either way. gen also moves with stream events and
// acknowledgements, which never change membership.
type Pair = { gen: number; member: number; read_up_to: number | undefined; unread: number | undefined; off?: boolean };

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
   * newest seen. While the person isn't on the board, nothing is noted: an event or an
   * acknowledgement from before they left can't bring it back.
   */
  note(id: string, gen: number, read_up_to: number | undefined, unread: number | undefined): void {
    const kept = this.pairs.get(id);
    if (kept?.off) return;
    if (!kept) {
      this.pairs.set(id, { gen, member: 0, read_up_to, unread });
      return;
    }
    const was = kept.read_up_to ?? -1;
    const now = read_up_to ?? -1;
    if (now > was || (now === was && gen > kept.gen)) {
      this.pairs.set(id, { gen: Math.max(gen, kept.gen), member: kept.member, read_up_to, unread });
    } else {
      kept.gen = Math.max(gen, kept.gen);
    }
  }

  /**
   * board returns b, a fresh read that started at generation gen, with the newest pair
   * kept for it. When b says the person isn't on the board (on_board false, or no read
   * position), the board's bookkeeping is cleared and b shows none. When the person is
   * on it again, a read that started after the one that cleared it starts afresh, with
   * nothing kept from before. A read that started before the newest one taken changes
   * neither membership nor bookkeeping: b shows what is kept.
   */
  board(b: Board, gen: number): Board {
    const kept = this.pairs.get(b.id);
    if (kept && gen < kept.member) return this.apply(b);
    if (!b.on_board || b.read_up_to === undefined) {
      this.pairs.set(b.id, { gen: Math.max(gen, kept?.gen ?? 0), member: gen, read_up_to: undefined, unread: undefined, off: true });
      return { ...b, read_up_to: undefined, unread: undefined };
    }
    if (!kept || kept.off) {
      this.pairs.set(b.id, { gen: Math.max(gen, kept?.gen ?? 0), member: gen, read_up_to: b.read_up_to, unread: b.unread });
    } else {
      this.note(b.id, gen, b.read_up_to, b.unread);
      this.pairs.get(b.id)!.member = gen;
    }
    return this.apply(b);
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
    if (kept?.off) return { ...b, read_up_to: undefined, unread: undefined };
    return kept ? { ...b, read_up_to: kept.read_up_to, unread: kept.unread } : b;
  }
}
