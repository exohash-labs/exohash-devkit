import type { NextConfig } from "next";

const BFF_URL = process.env.BFF_URL || "http://localhost:3100";
const CHAIN_API_URL = process.env.CHAIN_API_URL || "http://localhost:1317";

const nextConfig: NextConfig = {
  allowedDevOrigins: ["46.62.175.41"],
  async rewrites() {
    return [
      {
        source: "/api/bff/:path*",
        destination: `${BFF_URL}/:path*`,
      },
      {
        source: "/api/chain/:path*",
        destination: `${CHAIN_API_URL}/:path*`,
      },
    ];
  },
};

export default nextConfig;
