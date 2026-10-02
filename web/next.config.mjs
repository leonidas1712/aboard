// The UI is static files (output: 'export') that the aboard binary embeds and serves.
// Everything it shows comes from the public API and the event stream.
/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "export",
  images: { unoptimized: true },
};

export default nextConfig;
