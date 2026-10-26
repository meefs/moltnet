import type { APIRoute } from 'astro';
import { renderCard } from '../og/card.mjs';

// Link-preview image for the landing page.
export const GET: APIRoute = async () =>
  new Response(await renderCard({ kind: 'landing' }), { headers: { 'Content-Type': 'image/png' } });
