if (document.querySelector('.mermaid')) {
  const { default: mermaid } = await import('mermaid');

  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: 'base',
    themeVariables: {
      background: 'transparent',
      fontFamily: '"Geist Variable", system-ui, sans-serif',
      fontSize: '14px',
      primaryColor: '#0d100e',
      primaryTextColor: '#eef3ef',
      primaryBorderColor: '#2a322c',
      secondaryColor: '#0f2a1c',
      tertiaryColor: '#070908',
      lineColor: '#8a958d',
      textColor: '#b4beb7',
      clusterBkg: '#070908',
      clusterBorder: '#2a322c',
      edgeLabelBackground: '#070908',
      nodeBorder: '#2a322c',
      mainBkg: '#0d100e',
    },
    flowchart: {
      htmlLabels: false,
      curve: 'basis',
      padding: 16,
      nodeSpacing: 50,
      rankSpacing: 60,
    },
  });

  await mermaid.run({ querySelector: '.mermaid' });
}
