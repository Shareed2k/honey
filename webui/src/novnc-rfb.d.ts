declare module '@novnc/novnc' {
  type RFBInstance = {
    background: string;
    scaleViewport: boolean;
    resizeSession: boolean;
    disconnect: () => void;
    addEventListener: (type: string, listener: (ev: Event) => void) => void;
    removeEventListener: (type: string, listener: (ev: Event) => void) => void;
  };
  type RFBOptions = {
    wsProtocols?: string[];
    credentials?: { password?: string; username?: string };
  };
  const RFB: new (target: HTMLElement, url: string, options?: RFBOptions) => RFBInstance;
  export default RFB;
}
