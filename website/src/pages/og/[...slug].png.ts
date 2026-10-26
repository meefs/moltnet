import type { APIRoute, GetStaticPaths } from 'astro';
import { getCollection } from 'astro:content';
import { renderCard } from '../../og/card.mjs';

const SECTIONS: Record<string, string> = { guides: 'Guides', reference: 'Reference' };

// One link-preview image per docs page, at /og/<page id>.png.
export const getStaticPaths: GetStaticPaths = async () =>
  (await getCollection('docs')).map((entry) => ({
    params: { slug: entry.id },
    props: {
      title: entry.data.title,
      description: entry.data.description,
      section: SECTIONS[entry.id.split('/')[0]],
    },
  }));

export const GET: APIRoute = async ({ props }) =>
  new Response(await renderCard({ kind: 'page', ...props }), { headers: { 'Content-Type': 'image/png' } });
