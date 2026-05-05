import React from 'react';
import ComponentCreator from '@docusaurus/ComponentCreator';

export default [
  {
    path: '/honey/',
    component: ComponentCreator('/honey/', '7d4'),
    routes: [
      {
        path: '/honey/',
        component: ComponentCreator('/honey/', '0b6'),
        routes: [
          {
            path: '/honey/',
            component: ComponentCreator('/honey/', 'f8e'),
            routes: [
              {
                path: '/honey/cli/honey',
                component: ComponentCreator('/honey/cli/honey', '326'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_backends',
                component: ComponentCreator('/honey/cli/honey_backends', 'b61'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_completion',
                component: ComponentCreator('/honey/cli/honey_completion', '905'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_completion_bash',
                component: ComponentCreator('/honey/cli/honey_completion_bash', '44f'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_completion_fish',
                component: ComponentCreator('/honey/cli/honey_completion_fish', '0cc'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_completion_powershell',
                component: ComponentCreator('/honey/cli/honey_completion_powershell', '71c'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_completion_zsh',
                component: ComponentCreator('/honey/cli/honey_completion_zsh', '5cf'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_config',
                component: ComponentCreator('/honey/cli/honey_config', 'f35'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_cue-exec',
                component: ComponentCreator('/honey/cli/honey_cue-exec', '100'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_cue-validate',
                component: ComponentCreator('/honey/cli/honey_cue-validate', '305'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_mcp',
                component: ComponentCreator('/honey/cli/honey_mcp', '807'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_search',
                component: ComponentCreator('/honey/cli/honey_search', 'dc6'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/cli/honey_version',
                component: ComponentCreator('/honey/cli/honey_version', '1dc'),
                exact: true,
                sidebar: "tutorialSidebar"
              },
              {
                path: '/honey/',
                component: ComponentCreator('/honey/', '20f'),
                exact: true,
                sidebar: "tutorialSidebar"
              }
            ]
          }
        ]
      }
    ]
  },
  {
    path: '*',
    component: ComponentCreator('*'),
  },
];
