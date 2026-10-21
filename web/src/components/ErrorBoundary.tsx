// The root error boundary: a render error anywhere in the app shows what
// happened and a Reload button, never a blank page (lodge's frame would
// otherwise keep a dead app until the browser is reloaded).
import { Component, type ErrorInfo, type ReactNode } from "react";

/** State is the error the boundary caught, if any. */
interface State {
  error: Error | null;
}

/** ErrorBoundary catches a render error below it and shows it. */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  override state: State = { error: null };

  /** getDerivedStateFromError turns a thrown error into state. */
  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    // The console keeps the component stack for a developer with the app
    // open; the page itself shows the message alone.
    console.error("read-web: a render failed", error, info.componentStack);
  }

  override render(): ReactNode {
    const { error } = this.state;
    if (error === null) return this.props.children;
    return (
      <main role="alert" className="flex h-full flex-col items-start gap-3 bg-window p-[var(--pad-x)] text-primary">
        <h1 className="text-reader-subject font-semibold">Something went wrong</h1>
        <p className="text-reader-body text-secondary">{error.message}</p>
        <button
          type="button"
          onClick={() => window.location.reload()}
          className="h-7 rounded-md border border-separator px-2 text-sidebar-row text-primary hover:bg-selection-inactive"
        >
          Reload
        </button>
      </main>
    );
  }
}
