/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      colors: {
        lime: {
          400: '#a3ff47',
          500: '#84e030',
        },
        void: {
          900: '#07080f',
          800: '#0d0f1a',
          700: '#131625',
          600: '#1a1f35',
          500: '#222844',
        },
        steel: {
          300: '#c8d0e8',
          400: '#9aa3c0',
          500: '#6b7494',
        },
      },
      fontFamily: {
        display: ['Syne', 'sans-serif'],
        mono: ['JetBrains Mono', 'monospace'],
      },
    },
  },
  plugins: [],
}
