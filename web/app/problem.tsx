import { ApiError } from "./api";

// Problem shows a failed read, and what to do about it.
export default function Problem({ error }: { error: unknown }) {
  if (error instanceof ApiError && error.status === 401) {
    return (
      <p className="problem">
        This browser isn&apos;t logged in to Aboard, or its login has ended. Run <code>aboard open</code> in a terminal.
      </p>
    );
  }
  if (error instanceof ApiError) {
    return (
      <p className="problem">
        {error.message} {error.hint}
      </p>
    );
  }
  return <p className="problem">Couldn&apos;t reach the Aboard server. Start it with <code>aboard up</code>, then reload.</p>;
}

export function StarterBadge() {
  return (
    <span className="badge" title="Every member reads everything. Before adding more agents or people, run: aboard board policy recommended">
      starter policy
    </span>
  );
}
