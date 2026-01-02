/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./pkg/webui/web/templates/**/*.html",
    "./pkg/webui/web/static/**/*.js"
  ],
  theme: {
    extend: {
      colors: {
        'maestro-blue': '#1e40af',
        'maestro-green': '#059669',
        'maestro-yellow': '#d97706',
        'maestro-red': '#dc2626',
        'maestro-gray': '#6b7280',
      }
    },
  },
  plugins: [],
}