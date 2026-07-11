const { chromium } = require('playwright-extra');
const stealth = require('puppeteer-extra-plugin-stealth')();
chromium.use(stealth);

const url = process.argv[2];

if (!url) {
    console.log(JSON.stringify({ error: "Missing URL argument" }));
    process.exit(1);
}

(async () => {
    const browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    try {
        // 'networkidle' waits for zero network activity for 500ms — sites
        // with persistent background beacons/polling (common on
        // bot-detection/analytics-heavy pages) never reach it and this
        // reliably times out. 'domcontentloaded' fires once the DOM is
        // parsed; the explicit waitForTimeout below still gives
        // Cloudflare/Turnstile challenges time to resolve afterward.
        await page.goto(url, { waitUntil: 'domcontentloaded' });
        // Wait 5 seconds for Cloudflare/Turnstile challenges to resolve
        await page.waitForTimeout(5000); 
        
        const content = await page.content();
        console.log(JSON.stringify({ content: content }));
    } catch (e) {
        console.log(JSON.stringify({ error: e.message }));
    } finally {
        await browser.close();
    }
})();
