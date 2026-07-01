import { Layout, Menu, Typography, Alert, Button } from 'antd';
import type { MenuProps } from 'antd';
import {
  SearchOutlined, FileOutlined, CloudOutlined, SettingOutlined,
  PlayCircleOutlined, ApiOutlined, AppstoreOutlined, DatabaseOutlined, UnorderedListOutlined,
  CommentOutlined, RobotOutlined,
} from '@ant-design/icons';
import { RecipesTab } from './RecipesTab';
import { AppsTab } from './AppsTab';
import StudioWorkspace from './RecipeStudio/StudioWorkspace';
import { BackendsTab } from './tabs/BackendsTab';
import { FilesTab } from './tabs/FilesTab';
import { TunnelsTab } from './tabs/TunnelsTab';
import { LogsTab } from './tabs/LogsTab';
import { ConfigTab } from './tabs/ConfigTab';
import { ApiDocsTab } from './tabs/ApiDocsTab';
import { SearchTab } from './tabs/SearchTab';
import { FeedbackTab } from './tabs/FeedbackTab';
import { AgentTab } from './tabs/AgentTab';
import { useNavigation, type Tab } from './contexts/NavigationContext';
import { useAppContext } from './contexts/AppContext';
import { useTerminal } from './contexts/TerminalContext';

export function App() {
  const { tab, setTab } = useNavigation();
  const { tokenMsg, meta, backends, backErr } = useAppContext();
  const { terminals, isTerminalModalOpen, setIsTerminalModalOpen } = useTerminal();

  const menuItems: MenuProps['items'] = [
    { key: 'search',   icon: <SearchOutlined />,    label: 'Search' },
    { key: 'files',    icon: <FileOutlined />,       label: 'Files' },
    { key: 'backends', icon: <CloudOutlined />,      label: 'Backends' },
    { key: 'config',   icon: <SettingOutlined />,    label: 'Config' },
    { key: 'recipes',  icon: <PlayCircleOutlined />, label: 'Recipes' },
    { key: 'studio',   icon: <AppstoreOutlined />,   label: 'Recipe Studio' },
    { key: 'tunnels',  icon: <ApiOutlined />,        label: 'Tunnels' },
    { key: 'apps',     icon: <DatabaseOutlined />,      label: 'Apps & Proxies' },
    { key: 'logs',     icon: <UnorderedListOutlined />, label: 'Logs' },
    { key: 'feedback', icon: <CommentOutlined />,       label: 'Logs Feedback' },
    { key: 'agent',    icon: <RobotOutlined />,         label: 'AI Agent' },
    { key: 'api-docs', icon: <AppstoreOutlined />,      label: 'API Docs' },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Layout.Sider collapsible width={200} theme="dark">
        <div style={{ padding: '12px 16px', borderBottom: '1px solid #1d2535', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <svg viewBox="0 0 120 120" width="18" height="18" style={{ flexShrink: 0 }}>
            <defs>
              <linearGradient id="honeyGradSider" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#FFC107" />
                <stop offset="100%" stopColor="#F57C00" />
              </linearGradient>
            </defs>
            <polygon points="60,10 105,35 105,85 60,110 15,85 15,35" fill="url(#honeyGradSider)" />
            <polygon points="60,20 95,40 95,80 60,100 25,80 25,40" fill="#14171c" />
            <g transform="translate(38, 72)">
              <text fontFamily="monospace, Consolas, 'Courier New'" fontSize="36" fontWeight="900" fill="#FFC107" letterSpacing="-2">&gt;_</text>
            </g>
          </svg>
          <Typography.Text strong style={{ color: '#e6e6e6', fontSize: 14 }}>
            honey
          </Typography.Text>
          {meta && (
            <Typography.Text style={{ color: '#666', fontSize: 11, marginLeft: 6 }}>
              v{meta.version}
            </Typography.Text>
          )}
        </div>
        {tokenMsg && (
          <Alert message={tokenMsg} type="warning" banner style={{ fontSize: 11 }} />
        )}
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[tab]}
          items={menuItems}
          onSelect={({ key }) => setTab(key as Tab)}
          style={{ borderRight: 0 }}
        />
      </Layout.Sider>

      <Layout>
        <Layout.Content style={{ padding: '16px 20px', overflowY: 'auto', minHeight: 0 }}>
          <div style={{ display: tab === 'search' ? 'block' : 'none', height: '100%' }}>
            <SearchTab />
          </div>

          {tab === 'files' ? <FilesTab /> : null}
          {tab === 'backends' ? <BackendsTab backends={backends} error={backErr} /> : null}
          {tab === 'config' ? <ConfigTab /> : null}
          {tab === 'recipes' ? <RecipesTab /> : null}
          {tab === 'tunnels' ? <TunnelsTab /> : null}

          <div style={{ display: tab === 'studio' ? 'block' : 'none', height: '100%' }}>
            <StudioWorkspace />
          </div>

          {tab === 'apps' ? <AppsTab /> : null}

          <div style={{ display: tab === 'logs' ? 'flex' : 'none', flexDirection: 'column', height: '100%' }}>
            <LogsTab />
          </div>

          {tab === 'api-docs' ? <ApiDocsTab /> : null}
          {tab === 'feedback' ? <FeedbackTab /> : null}
          {tab === 'agent' ? <AgentTab /> : null}
        </Layout.Content>
      </Layout>

      {/* Floating terminal button */}
      {terminals.length > 0 && !isTerminalModalOpen && (
        <Button
          type="primary"
          shape="round"
          style={{ position: 'fixed', bottom: 32, right: 32, zIndex: 40 }}
          onClick={() => setIsTerminalModalOpen(true)}
        >
          🖥️ Open Terminals ({terminals.length})
        </Button>
      )}
    </Layout>
  );
}