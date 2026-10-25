#!/usr/bin/env node

/**
 * Rasterise the repository's Helm mark without adding an image dependency.
 * The supersampled geometry below mirrors public/helm-mark.svg: a 34-unit
 * disc split by a 3.5-unit H channel into three 9-unit columns, with 1.5-unit
 * radii where the crossbar meets the centre cards. The mark is drawn in white
 * on the in-app brand tile gradient, producing deterministic PNGs for Apple
 * Home Screen and Web App Manifest consumers.
 */

import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import { fileURLToPath } from 'node:url';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, '..');
const markPath = path.join(webRoot, 'public', 'helm-mark.svg');
const outputDir = path.join(webRoot, 'public', 'icons');
const sourceSvg = fs.readFileSync(markPath, 'utf8');
const samplesPerAxis = 4;

// Geometry from helm-mark.svg, in its 48-unit viewBox.
const centre = 24;
const discRadius = 17;
const channelHalf = 1.75;
const channelOffset = 6.25;
const cardRadius = 1.5;
const cardLeft = centre - channelOffset + channelHalf;
const cardRight = centre + channelOffset - channelHalf;

// The same 145deg gradient as the in-app `.brand-mark` tile.
const gradientStart = [130, 118, 255];
const gradientEnd = [92, 76, 237];
const markColor = [255, 255, 255];

if (!sourceSvg.includes(`A${discRadius} ${discRadius} 0 0 0 16 39Z`)) {
  throw new Error('helm-mark.svg geometry changed; update generate-pwa-icons.mjs to match it.');
}

/** Point-in-shape test for the disc minus its H-shaped channel. */
function markContains(x, y) {
  if (Math.hypot(x - centre, y - centre) > discRadius) return false;
  if (Math.abs(x - (centre - channelOffset)) < channelHalf) return false;
  if (Math.abs(x - (centre + channelOffset)) < channelHalf) return false;
  if (x <= cardLeft || x >= cardRight) return true;

  const dy = Math.abs(y - centre);
  if (dy < channelHalf) return false;
  // Round the two inner corners of each centre card.
  const cornerY = channelHalf + cardRadius;
  if (dy >= cornerY) return true;
  const cornerX = x < centre ? cardLeft + cardRadius : cardRight - cardRadius;
  if (x < centre ? x >= cornerX : x <= cornerX) return true;
  return Math.hypot(x - cornerX, dy - cornerY) <= cardRadius;
}

/** Rounded-square tile test in pixel space; a zero radius fills the canvas. */
function tileContains(px, py, size, radius) {
  if (!radius) return true;
  const qx = Math.max(Math.abs(px - size / 2) - (size / 2 - radius), 0);
  const qy = Math.max(Math.abs(py - size / 2) - (size / 2 - radius), 0);
  return Math.hypot(qx, qy) <= radius;
}

function gradientAt(px, py, size) {
  const angle = 145 * Math.PI / 180;
  const dx = Math.sin(angle);
  const dy = -Math.cos(angle);
  const length = size * (Math.abs(dx) + Math.abs(dy));
  const t = Math.max(0, Math.min(1, ((px - size / 2) * dx + (py - size / 2) * dy) / length + 0.5));
  return gradientStart.map((start, index) => start + (gradientEnd[index] - start) * t);
}

function rasterize(size, { markFraction, cornerFraction }) {
  const pixels = Buffer.alloc(size * size * 4);
  const markScale = size * markFraction / (discRadius * 2);
  const radius = size * cornerFraction;
  const sampleCount = samplesPerAxis * samplesPerAxis;

  for (let y = 0; y < size; y += 1) {
    for (let x = 0; x < size; x += 1) {
      const sum = [0, 0, 0];
      let covered = 0;
      for (let sampleY = 0; sampleY < samplesPerAxis; sampleY += 1) {
        for (let sampleX = 0; sampleX < samplesPerAxis; sampleX += 1) {
          const px = x + (sampleX + 0.5) / samplesPerAxis;
          const py = y + (sampleY + 0.5) / samplesPerAxis;
          if (!tileContains(px, py, size, radius)) continue;
          const sourceX = centre + (px - size / 2) / markScale;
          const sourceY = centre + (py - size / 2) / markScale;
          const color = markContains(sourceX, sourceY) ? markColor : gradientAt(px, py, size);
          for (let channel = 0; channel < 3; channel += 1) sum[channel] += color[channel];
          covered += 1;
        }
      }

      const offset = (y * size + x) * 4;
      for (let channel = 0; channel < 3; channel += 1) {
        pixels[offset + channel] = covered ? Math.round(sum[channel] / covered) : 0;
      }
      pixels[offset + 3] = Math.round(255 * covered / sampleCount);
    }
  }
  return pixels;
}

function crc32(value) {
  let crc = 0xffffffff;
  for (const byte of value) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function pngChunk(type, data) {
  const typeBytes = Buffer.from(type, 'ascii');
  const payload = Buffer.concat([typeBytes, data]);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(payload), 0);
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  return Buffer.concat([length, payload, checksum]);
}

function encodePng(size, pixels) {
  const scanlines = Buffer.alloc((size * 4 + 1) * size);
  for (let y = 0; y < size; y += 1) {
    const rowOffset = y * (size * 4 + 1);
    scanlines[rowOffset] = 0;
    pixels.copy(scanlines, rowOffset + 1, y * size * 4, (y + 1) * size * 4);
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8;
  header[9] = 6;
  header[10] = 0;
  header[11] = 0;
  header[12] = 0;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', header),
    pngChunk('IDAT', zlib.deflateSync(scanlines, { level: 9 })),
    pngChunk('IEND', Buffer.alloc(0))
  ]);
}

fs.mkdirSync(outputDir, { recursive: true });
// Apple and maskable icons must be opaque and full-bleed; the platform applies
// its own mask, and maskable content stays inside the central 80% safe zone.
// The plain manifest icons carry the in-app tile's rounded corners.
for (const [name, size, options] of [
  ['icon-180.png', 180, { markFraction: 0.6, cornerFraction: 0 }],
  ['icon-192.png', 192, { markFraction: 0.6, cornerFraction: 0.28 }],
  ['icon-512.png', 512, { markFraction: 0.6, cornerFraction: 0.28 }],
  ['icon-512-maskable.png', 512, { markFraction: 0.52, cornerFraction: 0 }]
]) {
  fs.writeFileSync(path.join(outputDir, name), encodePng(size, rasterize(size, options)));
}

console.log(`Generated Helm PWA icons from ${path.relative(webRoot, markPath)} in ${path.relative(webRoot, outputDir)}.`);
