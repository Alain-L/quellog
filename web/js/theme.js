// Theme management for quellog web app

const THEME_KEY = 'quellog-theme';

export function getPreferredTheme() {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved) return saved;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function setTheme(theme) {
    const htmlEl = document.documentElement;
    const iconSun = document.getElementById('iconSun');
    const iconMoon = document.getElementById('iconMoon');

    htmlEl.dataset.theme = theme;
    // Show sun in dark mode (click to switch to light), moon in light mode (click to switch to dark)
    if (iconSun) iconSun.style.display = theme === 'dark' ? 'block' : 'none';
    if (iconMoon) iconMoon.style.display = theme === 'light' ? 'block' : 'none';
    localStorage.setItem(THEME_KEY, theme);
}

export function toggleTheme() {
    const current = document.documentElement.dataset.theme || 'light';
    setTheme(current === 'dark' ? 'light' : 'dark');
}

export function initTheme() {
    // Initialize theme
    setTheme(getPreferredTheme());

    // Listen for system theme changes
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
        if (!localStorage.getItem(THEME_KEY)) {
            setTheme(e.matches ? 'dark' : 'light');
        }
    });
}
