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
 */
export function watchChanges(transport: Transport, onChange: () => void, retryMs: number = RETRY_MS): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const stopRetrying = () => {
    if (timer !== undefined) {
      clearTimeout(timer);
      timer = undefined;
    }
  };
  const subscribe = () => {
    if (stopped) return;
    transport.call("events.subscribe", {}).then(stopRetrying, () => {
      if (stopped) return;
      stopRetrying();
      timer = setTimeout(subscribe, retryMs);
    });
  };

  const offEvent = transport.onEvent((event) => {
    if (event.event === ITEMS_CHANGED) onChange();
  });
  const offClose = transport.onClose(() => {
    if (stopped) return;
    stopRetrying();
    subscribe();
  });
  subscribe();

  return () => {
    stopped = true;
    stopRetrying();
    offEvent();
    offClose();
  };
}
