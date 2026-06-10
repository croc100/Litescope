// VS Code WebView API bridge
// In VS Code: uses acquireVsCodeApi()
// In browser dev mode: uses mock

interface VsCodeApi {
  postMessage(message: unknown): void;
  getState(): unknown;
  setState(state: unknown): void;
}

declare global {
  interface Window {
    __LITESCOPE__?: {
      initialPath?: string;
      vscodeApi?: boolean;
    };
    acquireVsCodeApi?: () => VsCodeApi;
  }
}

let _api: VsCodeApi | null = null;

export function getVsCodeApi(): VsCodeApi {
  if (_api) return _api;
  if (window.acquireVsCodeApi) {
    _api = window.acquireVsCodeApi();
  } else {
    // Dev mode mock
    _api = {
      postMessage: (msg) => console.log("[mock postMessage]", msg),
      getState: () => ({}),
      setState: (s) => console.log("[mock setState]", s),
    };
  }
  return _api;
}

export function postMessage(msg: { type: string; payload?: unknown }) {
  getVsCodeApi().postMessage(msg);
}

export const initialPath: string =
  window.__LITESCOPE__?.initialPath ?? "";
