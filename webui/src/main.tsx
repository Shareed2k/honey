import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider, theme } from 'antd';
import { App } from './App';
import { RootProvider } from './contexts';
import { RedeemAccessView } from './RedeemAccessView';
import '@xterm/xterm/css/xterm.css';
import './app.css';

// A share-link recipient has no honey session — detect an access code from
// either `?access=<code>` or a `/access/<code>` path BEFORE anything that
// assumes an authed session (RootProvider, App) is mounted.
function detectAccessCode(): string | null {
  const fromQuery = new URLSearchParams(window.location.search).get('access');
  if (fromQuery) {
    return fromQuery;
  }
  const match = /^\/access\/([^/]+)\/?$/.exec(window.location.pathname);
  return match ? decodeURIComponent(match[1]) : null;
}

const accessCode = detectAccessCode();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorBgBase: '#0f1115',
          colorPrimary: '#3d6fb8',
          colorBgContainer: '#141922',
          colorBorderSecondary: '#2a3140',
          borderRadius: 4,
          fontFamily: 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif',
        },
      }}
    >
      {accessCode ? (
        <RedeemAccessView code={accessCode} />
      ) : (
        <RootProvider>
          <App />
        </RootProvider>
      )}
    </ConfigProvider>
  </React.StrictMode>,
);
