FROM mcr.microsoft.com/playwright:v1.44.0-jammy

# Create an app directory
WORKDIR /app

# Initialize a package.json and install the stealth libraries ONCE during the build
RUN npm init -y && \
    npm install playwright playwright-extra puppeteer-extra-plugin-stealth

# Copy our dedicated fetch script into the image
COPY fetch.js /app/fetch.js
