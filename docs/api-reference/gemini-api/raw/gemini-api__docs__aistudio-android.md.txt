# Build Android Apps in Google AI Studio

Google AI Studio lets you build native Android apps from a natural language
prompt. Describe the app you want, and the
[Antigravity Agent](https://ai.google.dev/gemini-api/docs/aistudio-build-mode#antigravity-agent)
generates a complete Kotlin and [Jetpack Compose](https://developer.android.com/develop/ui/compose)
project. From your browser, you can preview your app in a browser-based Android emulator, install
it on a physical device, and publish it for testing.

> [!NOTE]
> **Note:** Android apps in AI Studio are client-side only. Features that require a server-side runtime, such as [Firebase integration](https://ai.google.dev/gemini-api/docs/aistudio-fullstack#firebase-integration), [Google Workspace APIs](https://ai.google.dev/gemini-api/docs/aistudio-fullstack#workspace), [secrets management](https://ai.google.dev/gemini-api/docs/aistudio-fullstack#secrets), and [multiplayer](https://ai.google.dev/gemini-api/docs/aistudio-fullstack#multiplayer), are available for web apps only.

## Get started

To start building an Android app:

1. Go to [Build mode](https://aistudio.google.com/apps) in Google AI Studio using the left-hand navigation panel.
2. Select **Android** from the platform picker.
3. Enter a prompt describing the app you want to build (for example, *"Create a daily task tracker with local storage"* or *"Build a simple calculator"*).
4. The agent generates the project and launches it in the browser-based Android emulator.

You can then iterate on your app using the chat panel, just like the web
experience. The agent manages all files in your Android project and propagates
changes across the codebase.

## Browser-based Android emulator

The Android emulator runs entirely in the cloud and streams to your browser.
You don't need to install the Android SDK, Android Studio, or a local emulator.

The emulator provides:

- **Pixel-like device simulation**: tap, scroll, and interact with your app just like on a real device.
- **Rotation support**: toggle between portrait and landscape orientation.
- **Live preview**: when the agent makes code changes, the app rebuilds and the emulator refreshes automatically.

### Emulator limitations

The browser-based emulator doesn't support all hardware features. The following
are not available in the emulator:

- Camera and photo capture
- NFC and Bluetooth
- GPS (location is simulated)
- Google Play services (Google Sign-In, Maps, and other Play services features work on a real device but not in the emulator)

## Install on a device with ADB

You can install the built APK directly on a physical Android device connected
to your computer using USB. This uses
[WebUSB](https://developer.chrome.com/docs/capabilities/usb) to
communicate with your device through the browser. No local ADB installation
is required.

### Prerequisites

- A Chrome or Edge browser that supports WebUSB.
- An Android device with [Developer Options and USB Debugging](https://developer.android.com/studio/debug/dev-options) enabled.
- A USB cable connecting your device to your computer.

### Install the app on your device

1. Click **Install on Device** in the preview panel.
2. Select your Android device from the browser's USB device picker.
3. The APK is transferred and installed on your device.
4. The app launches automatically.

> [!NOTE]
> **Note:** Your device may show an **"Allow USB debugging"** prompt the first time you connect. You must accept this to proceed.

## Publish to the Play Store

You can publish your Android app to the
[Google Play Console](https://play.google.com/console) internal
testing track, which lets you distribute the app to up to 100 testers.

### Prerequisites

- A [Google Play Developer account](https://play.google.com/console/signup) (requires a one-time $25 registration fee).
- A completed developer profile in the Play Console.

### Publish your app

1. Open **Settings \> Publish** in Google AI Studio.
2. Click **Publish to Play Store**.
3. Authenticate with your Google Play Developer account.
4. AI Studio signs the APK, creates the app listing (or uploads a new version), and publishes to the internal testing track.
5. You receive a link to share with your testers.

AI Studio manages APK signing automatically using a managed keystore. You can
customize the app listing (icon, screenshots, description) later in the Play
Console.

> [!NOTE]
> **Note:** Publishing is limited to the **internal testing track** only. Production releases must be managed directly in the Google Play Console.

## What's generated

When you build an Android app, the agent generates a standard Gradle-based
project with the following structure:

- **Build configuration** : `build.gradle.kts` files (project and app level) using Kotlin DSL.
- **UI layer** : [Jetpack Compose](https://developer.android.com/develop/ui/compose) components with [Material 3](https://m3.material.io/) theming.
- **Architecture**: single-activity architecture with ViewModels and data classes.
- **Resources** : `AndroidManifest.xml`, drawables, strings, and other Android resources.

The agent automatically manages Gradle dependencies, adding packages from Maven
and Google repositories as needed.

You can view and edit the generated code using the **Code** tab in the preview
panel. To continue development in Android Studio, download the project as a
**ZIP file**.

## Limitations

Android app building in AI Studio has the following limitations:

### Platform limitations

- **Client-side only**: Android apps don't include a server-side component. Features requiring a server runtime (secrets management, multiplayer, Firebase, Google Workspace APIs) are not available.
- **Single-activity architecture**: only single-activity, single-module projects are supported.
- **Jetpack Compose only**: apps use Kotlin and Jetpack Compose. Java and XML layouts are not supported.
- **No NDK or native code**: C and C++ code is not supported.
- **No Wear OS or Android TV**: only phone and tablet form factors are supported.

### Export limitations

- **ZIP download only**: you can download the project as a ZIP file. GitHub export is not yet available for Android projects.

## What's next

- [Build apps in Google AI Studio](https://ai.google.dev/gemini-api/docs/aistudio-build-mode)
- [Developing Full-Stack Apps](https://ai.google.dev/gemini-api/docs/aistudio-fullstack) (web)
- See examples in the [App Gallery](https://aistudio.google.com/apps?source=showcase).