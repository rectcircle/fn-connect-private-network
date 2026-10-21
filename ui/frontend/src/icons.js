// Framework-free fallback for Linux. macOS replaces these with native SF Symbols.
const paths={
 'stop.fill':'<rect x="5" y="5" width="14" height="14" rx="2" fill="black" stroke="none"/>',
 'bolt.fill':'<path d="M14 2 5 14h6l-1 8 9-13h-6z" fill="black" stroke="none"/>',
 'person.badge.key':'<circle cx="9" cy="7" r="4"/><path d="M2 22v-3a7 7 0 0 1 13-3M18 15v7m-2-2h4"/><circle cx="18" cy="12" r="3"/>',
 'arrow.clockwise':'<path d="M19 7a8 8 0 1 0 1 9M19 2v6h-6"/>',
 'arrow.left.arrow.right':'<path d="M3 7h18M3 7l5-5M3 7l5 5M21 17H3m18 0-5-5m5 5-5 5"/>',
 globe:'<circle cx="12" cy="12" r="10"/><ellipse cx="12" cy="12" rx="4" ry="10"/><path d="M2 12h20M4 6h16M4 18h16"/>',
 'doc.on.doc':'<path d="M8 3h8l4 4v12H8zM16 3v5h4M4 7v16h12v-4"/>',
 folder:'<path d="M2 5h7l2 3h11v13H2zM2 11h20"/>',
 power:'<path d="M12 1v11M6 4a10 10 0 1 0 12 0"/>',
 'arrow.up.right.square':'<rect x="2" y="2" width="20" height="20" rx="3"/><path d="M8 16 16 8M8 8h8v8"/>',
 'arrow.right':'<path d="M2 12h20m-8-8 8 8-8 8"/>',
 'arrow.left':'<path d="M22 12H2m8-8-8 8 8 8"/>'
};
export const fallbackSymbols=Object.fromEntries(Object.entries(paths).map(([name,path])=>[name,'data:image/svg+xml,'+encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="black" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${path}</svg>`)]));
