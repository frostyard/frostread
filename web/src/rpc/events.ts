// readd's changes over lodge-client's rpc subpath
// (@frostyard/lodge-client/rpc, docs/specs/api.md, Notifications).
//
// The subpath hands every event to its handlers, in the kit's envelope, and
// reconnects on its own. A new connection is a new session on readd, so the
// subscription to events.subscribe is made again after each drop.
import type { Transport } from "@frostyard/lodge-client/rpc";

/** ITEMS_CHANGED is the event readd sends after any change. */
export const ITEMS_CHANGED = "items.changed";

/** RETRY_MS is how often a dropped connection asks to subscribe again. */
export const RETRY_MS = 1_000;

/**
 * watchChanges calls onChange for each items.changed, and subscribes to
 * them now and after every reconnect. A connection that is not up yet
 * refuses the call; it asks again, with a delay, until one takes.
 * onReconnected, when given, runs when a subscribe takes after a drop or
 * a failed subscribe (not after a first subscribe that takes): the desktop
 * refreshes what it missed.
 */
export function watchChanges(
  transport: Transport,
  onChange: () => void,
  retryMs: number = RETRY_MS,
  onReconnected?: () => void,
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  // missed says a subscribe failed, or the connection dropped, since the
  // last one that took: the next one that takes is a reconnect, and
  // onReconnected refreshes what the window missed meanwhile.
  let missed = false;
  // generation rises at each drop: an answer to a subscribe sent before
  // the latest drop belongs to a connection that is gone, and counts for
  // nothing.
  let generation = 0;

  const stopRetrying = () => {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  };
  // One timer at most: a new retry replaces the one that waits.
  const retry = () => {
    stopRetrying();
    timer = setTimeout(subscribe, retryMs);
  };
  const subscribe = () => {
    if (stopped) return;
    const mine = generation;
    transport.call("events.subscribe", {}).then(
      () => {
        if (stopped || mine !== generation) return;
        stopRetrying();
        if (missed) {
          missed = false;
          onReconnected?.();
        }
      },
      () => {
        if (stopped || mine !== generation) return;
        missed = true;
        retry();
      },
    );
  };

  const offEvent = transport.onEvent((event) => {
    if (event.event === ITEMS_CHANGED) onChange();
  });
  const offClose = transport.onClose(() => {
    if (stopped) return;
    generation++;
    missed = true;
    // Without onReconnected (lodge's frame), the first try after a drop
    // is at once, as it always was. With it (the desktop), this watcher is
    // the window's only retry schedule, and its first try waits retryMs,
    // as the desktop's banner says.
    if (onReconnected === undefined) {
      stopRetrying();
      subscribe();
    } else {
      retry();
    }
  });
  subscribe();

  return () => {
    stopped = true;
    stopRetrying();
    offEvent();
    offClose();
  };
}
