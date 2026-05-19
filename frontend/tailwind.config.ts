import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        ink: "#16211f",
        mist: "#eef4f1",
        steel: "#4d6470",
        ocean: "#256f8f",
        moss: "#4f7f52",
        clay: "#b45f3c",
      },
      boxShadow: {
        soft: "0 16px 50px rgba(28, 45, 50, 0.12)",
      },
    },
  },
  plugins: [],
};

export default config;
