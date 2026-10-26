import { defineRouteMiddleware } from '@astrojs/starlight/route-data';

// Adds each docs page's link-preview image (src/pages/og/) and matches the
// landing page's og:locale.
export const onRequest = defineRouteMiddleware((context) => {
  const { entry, head } = context.locals.starlightRoute;
  const image = new URL(`/og/${entry.id}.png`, context.site).href;
  const alt = `${entry.data.title}, Moltnet docs`;
  for (const tag of head) {
    if (tag.tag === 'meta' && tag.attrs?.property === 'og:locale') tag.attrs.content = 'en_US';
  }
  head.push(
    { tag: 'meta', attrs: { property: 'og:image', content: image } },
    { tag: 'meta', attrs: { property: 'og:image:width', content: '1200' } },
    { tag: 'meta', attrs: { property: 'og:image:height', content: '630' } },
    { tag: 'meta', attrs: { property: 'og:image:alt', content: alt } },
    { tag: 'meta', attrs: { name: 'twitter:image', content: image } },
    { tag: 'meta', attrs: { name: 'twitter:image:alt', content: alt } },
  );
});
