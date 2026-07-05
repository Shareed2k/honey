import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider, theme } from 'antd';
import { App } from './App';
import { RootProvider } from './contexts';
import '@xterm/xterm/css/xterm.css';
import './app.css';

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
      <RootProvider>
        <App />
      </RootProvider>
    </ConfigProvider>
  </React.StrictMode>,
);
