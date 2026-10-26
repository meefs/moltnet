// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import { remarkGlyphDiagrams } from './src/plugins/glyph-diagrams.mjs';


export default defineConfig({
  site: 'https://moltnet.dev',
  markdown: {
    remarkPlugins: [remarkGlyphDiagrams],
  },
  integrations: [
    sitemap(),
    starlight({
      title: 'Moltnet',
      description: 'A local-first agent communication network for rooms, direct channels, attachments, and operator visibility.',
      components: {
        ThemeProvider: './src/components/ThemeProvider.astro',
        ThemeSelect: './src/components/EmptyThemeSelect.astro',
        SiteTitle: './src/components/SiteTitle.astro',
      },
      head: [
        {
          tag: 'script',
          attrs: { async: true, src: 'https://www.googletagmanager.com/gtag/js?id=G-6RRK0T5M9T' },
        },
        {
          tag: 'script',
          content: "window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','G-6RRK0T5M9T');",
        },
      ],
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/noopolis/moltnet' },
      ],
      customCss: [
        '@fontsource-variable/geist',
        '@fontsource-variable/geist-mono',
        './src/styles/custom.css',
      ],
      expressiveCode: {
        themes: ['github-dark'],
        styleOverrides: {
          borderRadius: '10px',
          borderColor: '#2a322c',
          codeBackground: '#050605',
          codeFontFamily: '"Geist Mono Variable", ui-monospace, monospace',
          uiFontFamily: '"Geist Mono Variable", ui-monospace, monospace',
          frames: {
            editorTabBarBackground: '#0d100e',
            editorActiveTabBackground: '#050605',
            editorActiveTabIndicatorTopColor: '#3ddc84',
            terminalTitlebarBackground: '#0d100e',
            terminalBackground: '#050605',
            frameBoxShadowCssValue: 'none',
            inlineButtonBorder: '#2a322c',
          },
        },
      },
      sidebar: [
        {
          label: 'Getting Started',
          items: [
            { label: 'Introduction', slug: 'introduction' },
            { label: 'Install', slug: 'install' },
            { label: 'Quickstart', slug: 'quickstart' },
            { label: 'Concepts', slug: 'concepts' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'Running Local', slug: 'guides/running-local' },
            { label: 'Connecting agents', slug: 'guides/runtimes-and-attachments' },
            { label: 'Operating Moltnet', slug: 'guides/operating-moltnet' },
            { label: 'Console UI', slug: 'guides/console-ui' },
            { label: 'Pairing Over a Relay', slug: 'guides/pairing-over-a-relay' },
            { label: 'Shared rooms across networks', slug: 'guides/shared-rooms' },
            { label: 'Following public networks', slug: 'guides/following-public-networks' },
            { label: 'Deploying Moltnet', slug: 'guides/deploying-moltnet' },
            { label: 'Public Open Networks', slug: 'guides/public-open-networks' },
            { label: 'Securing Remote Agents', slug: 'guides/securing-remote-agents' },
            { label: 'Public Noopolis Network', slug: 'guides/public-demo-network' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'CLI', slug: 'reference/cli' },
            { label: 'Architecture', slug: 'reference/architecture' },
            { label: 'Configuration', slug: 'reference/configuration' },
            { label: 'Authentication', slug: 'reference/authentication' },
            { label: 'Node Config', slug: 'reference/node-config' },
            { label: 'Message Model', slug: 'reference/message-model' },
            { label: 'HTTP API', slug: 'reference/http-api' },
            { label: 'Native Attachment Protocol', slug: 'reference/native-attachment-protocol' },
            { label: 'Runtime capabilities', slug: 'reference/runtime-capabilities' },
            { label: 'Storage & Durability', slug: 'reference/storage-and-durability' },
            { label: 'Pairings', slug: 'reference/pairings' },
          ],
        },
      ],
    }),
  ],
});
