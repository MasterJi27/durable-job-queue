const { chromium } = require('/Users/raghavkathuria/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const path = require('path');
const fs = require('fs');

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

(async () => {
  const mediaDir = path.resolve(__dirname, '../media');
  const screensDir = path.join(mediaDir, 'screenshots');
  const recordDir = path.join(mediaDir, 'recordings');

  fs.mkdirSync(screensDir, { recursive: true });
  fs.mkdirSync(recordDir, { recursive: true });

  console.log('Launching browser with Chrome channel...');
  const browser = await chromium.launch({
    channel: 'chrome',
    headless: true,
  });

  // --- PART 1: High-Resolution Retina Screenshots (scale factor 2) ---
  console.log('--- Capturing High-Res Retina Screenshots ---');
  const photoContext = await browser.newContext({
    viewport: { width: 1440, height: 920 },
    deviceScaleFactor: 2,
  });

  const photoPage = await photoContext.newPage();

  // 1. Overview Dashboard
  await photoPage.goto('http://127.0.0.1:8080/#overview', { waitUntil: 'networkidle' });
  await sleep(1500);
  await photoPage.screenshot({ path: path.join(screensDir, '01_dashboard_overview.png'), fullPage: false });
  console.log('Saved 01_dashboard_overview.png');

  // 2. Create Job Modal with Preset
  await photoPage.click('[data-new-job]');
  await sleep(600);
  await photoPage.click('button[data-preset="retry"]');
  await sleep(400);
  await photoPage.screenshot({ path: path.join(screensDir, '02_create_job_modal.png'), fullPage: false });
  console.log('Saved 02_create_job_modal.png');

  // Submit the job
  await photoPage.click('#submit-job');
  await sleep(1500);

  // 3. All Jobs View
  await photoPage.click('a[data-nav="jobs"]');
  await sleep(1000);
  await photoPage.screenshot({ path: path.join(screensDir, '03_all_jobs_view.png'), fullPage: false });
  console.log('Saved 03_all_jobs_view.png');

  // 4. Job Inspector Drawer
  const inspectBtn = await photoPage.$('.job-key');
  if (inspectBtn) {
    await inspectBtn.click();
    await sleep(1000);
    await photoPage.screenshot({ path: path.join(screensDir, '04_job_inspector_drawer.png'), fullPage: false });
    console.log('Saved 04_job_inspector_drawer.png');
    // Close dialog
    await photoPage.click('[data-close="detail-dialog"]');
    await sleep(500);
  }

  // 5. Simulation Lab (initial state)
  await photoPage.click('a[data-nav="simulation"]');
  await sleep(1000);
  await photoPage.screenshot({ path: path.join(screensDir, '05_simulation_lab_config.png'), fullPage: false });
  console.log('Saved 05_simulation_lab_config.png');

  // 6. Simulation Lab (run and pass invariants)
  await photoPage.click('#run-simulation');
  await photoPage.waitForSelector('#simulation-results:not([hidden])', { timeout: 15000 });
  await sleep(1500);
  await photoPage.screenshot({ path: path.join(screensDir, '06_simulation_lab_results.png'), fullPage: false });
  console.log('Saved 06_simulation_lab_results.png');

  // 7. Architecture modal ("How it works")
  await photoPage.click('#about-button');
  await sleep(600);
  await photoPage.screenshot({ path: path.join(screensDir, '07_architecture_modal.png'), fullPage: false });
  console.log('Saved 07_architecture_modal.png');
  await photoPage.click('[data-close="about-dialog"]');
  await sleep(500);

  await photoContext.close();

  // --- PART 2: Video Walkthrough Recording ---
  console.log('--- Recording Complete Application Walkthrough Video ---');
  const tempVideoDir = path.join(recordDir, 'temp_video');
  fs.mkdirSync(tempVideoDir, { recursive: true });

  const videoContext = await browser.newContext({
    viewport: { width: 1360, height: 850 },
    recordVideo: {
      dir: tempVideoDir,
      size: { width: 1360, height: 850 },
    },
  });

  const page = await videoContext.newPage();

  // Step 1: Open overview dashboard
  console.log('Video step 1: Overview page');
  await page.goto('http://127.0.0.1:8080/#overview', { waitUntil: 'networkidle' });
  await sleep(2500);

  // Step 2: Hover over metrics cards smoothly
  console.log('Video step 2: Metrics interaction');
  await page.hover('.metric.succeeded');
  await sleep(1000);
  await page.hover('.metric.dead');
  await sleep(1000);
  await page.hover('.metric.pending');
  await sleep(1000);

  // Step 3: Open New Job modal and try presets
  console.log('Video step 3: New Job modal');
  await page.click('[data-new-job]');
  await sleep(1200);

  await page.hover('button[data-preset="retry"]');
  await sleep(800);
  await page.click('button[data-preset="retry"]');
  await sleep(1200);

  await page.hover('button[data-preset="slow"]');
  await sleep(800);
  await page.click('button[data-preset="slow"]');
  await sleep(1200);

  await page.hover('button[data-preset="echo"]');
  await sleep(800);
  await page.click('button[data-preset="echo"]');
  await sleep(1200);

  // Submit job
  console.log('Video step 4: Submit job');
  await page.click('#submit-job');
  await sleep(2500);

  // Step 5: Navigate to All Jobs
  console.log('Video step 5: All jobs navigation');
  await page.click('a[data-nav="jobs"]');
  await sleep(2000);

  // Filter tabs
  await page.click('button[data-status="succeeded"]');
  await sleep(1500);
  await page.click('button[data-status="dead"]');
  await sleep(1500);
  await page.click('button[data-status=""]');
  await sleep(1500);

  // Step 6: Open Job Inspector on a job
  console.log('Video step 6: Inspect job');
  const firstJob = await page.$('.job-key');
  if (firstJob) {
    await firstJob.click();
    await sleep(2500);
    // Scroll inside detail body
    await page.evaluate(() => {
      const el = document.querySelector('#detail-body');
      if (el) el.scrollTop = 200;
    });
    await sleep(1500);
    await page.click('[data-close="detail-dialog"]');
    await sleep(1000);
  }

  // Step 7: Open Simulation Lab and run failure simulation
  console.log('Video step 7: Simulation Lab');
  await page.click('a[data-nav="simulation"]');
  await sleep(2000);

  await page.hover('#run-simulation');
  await sleep(1000);
  await page.click('#run-simulation');
  await page.waitForSelector('#simulation-results:not([hidden])', { timeout: 15000 });
  await sleep(3500);

  // Step 8: View Architecture & recovery info
  console.log('Video step 8: Architecture modal');
  await page.click('#about-button');
  await sleep(3000);
  await page.click('[data-close="about-dialog"]');
  await sleep(1500);

  // Step 9: Return to Overview to wrap up
  console.log('Video step 9: Finish on Overview');
  await page.click('a[data-nav="overview"]');
  await sleep(2500);

  await page.close();
  await videoContext.close();
  await browser.close();

  // Find recorded video file
  const videoFiles = fs.readdirSync(tempVideoDir).filter(f => f.endsWith('.webm'));
  if (videoFiles.length > 0) {
    const rawVideo = path.join(tempVideoDir, videoFiles[0]);
    const finalWebm = path.join(recordDir, 'durable_queue_demo.webm');
    fs.copyFileSync(rawVideo, finalWebm);
    console.log(`Video recorded: ${finalWebm}`);

    const { execSync } = require('child_process');
    const finalMp4 = path.join(recordDir, 'durable_job_queue_demo.mp4');
    const finalGif = path.join(recordDir, 'durable_job_queue_demo.gif');

    console.log('Converting to MP4 for LinkedIn / GitHub video...');
    execSync(`ffmpeg -y -i "${finalWebm}" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "${finalMp4}"`, { stdio: 'inherit' });
    console.log(`MP4 created: ${finalMp4}`);

    console.log('Generating optimized GIF for GitHub README embedding...');
    execSync(`ffmpeg -y -i "${finalWebm}" -vf "fps=12,scale=1080:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer" "${finalGif}"`, { stdio: 'inherit' });
    console.log(`GIF created: ${finalGif}`);
  } else {
    console.error('No video file produced!');
  }

  console.log('Screen capture and recording complete!');
})();
