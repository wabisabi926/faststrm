/** @type {import('tailwindcss').Config} */
// Tailwind CLI 构建期配置（非运行时）：扫描 .templ 文件类名，输出最小化 CSS
module.exports = {
  content: [
    "./internal/web/**/*.templ",
    "./internal/web/**/*.go",
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#eff6ff',
          500: '#3b82f6',
          600: '#2563eb',
          700: '#1d4ed8',
        },
      },
      fontFamily: {
        sans: ['ui-sans-serif', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'sans-serif'],
        mono: ['ui-monospace', 'Cascadia Code', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
