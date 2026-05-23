# Develop Full-Stack Apps in Google AI Studio

Google AI Studio now supports full-stack development, enabling you to build
applications that go beyond client-side prototypes. With a
server-side runtime, you can manage secrets, connect to external APIs, and build
real-time multiplayer experiences.

## Server-side runtime

Google AI Studio applications can now include a server-side component (Node.js).
This lets you:

- **Execute server-side logic**: Run code that shouldn't be exposed to the client.
- **Access npm packages** : The [Antigravity Agent](https://antigravity.google/docs/agent) can install and use packages from the vast npm ecosystem.
- **Handle secrets**: Securely use API keys and credentials.

### Use npm packages

You don't need to manually run `npm install`. Simply ask the Agent to add
functionality that requires a package, and it will handle the installation and
import.

**Example** : \> "Use `axios` to fetch data from the external API."

## Manage secrets securely

With server-side code and secrets management, you can now build apps that
interact with the world.

### Gemini API key

When you create a new app that uses the Gemini API, AI Studio automatically
configures your `GEMINI_API_KEY` as a server-side secret --- no manual setup
required. You can view this key in the **Secrets** panel in Settings. Your
app's Gemini API calls are made from server-side code using this key, so
it is never exposed in the browser.

### Third-party API keys

For other services, you can add API keys manually:

- **Third-party APIs**: Connect to services like Stripe, SendGrid, or custom REST APIs.
- **Databases**: Connect to external databases (e.g., via Supabase, Firebase, or MongoDB Atlas) to persist data beyond the session.

When building real-world apps, you often need to connect to third-party services
(like Twilio, Slack, or databases) that require API keys. You can add keys
manually with the following steps:

1. **Add a secret** : Go to the **Settings** menu in Google AI Studio and look for the Secrets section.
2. **Store your key**: Add your API keys or secret tokens here.
3. **Access in code**: The Agent can write server-side code that accesses these secrets securely (typically via environment variables), ensuring they are never exposed to the client-side browser.

When needed, the agent will
also show a card in the chat prompting you to add keys whenever a new secret is
needed or when a key new key is detected in the project's env variables.

### Firebase integration for database \& authentication

Google AI Studio now makes it easy to add a database or authentication to your
app through a
[Firebase integration](https://firebase.google.com/docs/ai-assistance/ai-studio-integration).
The Antigravity Agent can provision and set up the following services for you
automatically:

- **Firestore database**: a flexible, scalable NoSQL cloud database to store and sync data for client- and server-side development.
- **Firebase Authentication**: allow your users to securely sign in to your application using "Sign in with Google" flows.

Simply ask the agent to "add a database to my app" or "set up Google Sign-in",
and it will handle the necessary configuration and code generation for you.

Firebase lets you start for free, and optionally scale with a paid account
whenever you're ready for more quota or to use paid features.

## Google Workspace APIs

Google AI Studio lets you build apps that connect to Google Workspace APIs, so
your users can work with their real data: emails, spreadsheets, documents,
calendar events, and more, all within your app. You no longer need to set up a Google Cloud project, configure OAuth, or manage your API manually.

### How it works

You can add a Workspace integration in two ways:

- **Describe it in the chat panel** : Just tell the agent what you want in the chat panel at the bottom. For example, *"Build an expense tracker that logs receipts to my Google Sheet"* or *"Create a dashboard that summarizes my unread Gmail messages."*
- **Select from the integrations panel** : Open the **Integrations** panel in the right-hand sidebar of Build mode and enable the Workspace app you want to connect.

When you add a Workspace app, AI Studio automatically:

1. Wires up the necessary Google API for your app.
2. Generates the server-side code to call the API.
3. Adds a secure "Sign in with Google" flow so end users of your app can authorize access to their own data.

### Supported apps

The following Google Workspace apps are available:

| App | What you can build |
|---|---|
| Google Calendar | Read, create, and manage events and calendars |
| Google Chat | Read and interact with conversations and group spaces |
| Google Docs | Create, read, update, and format documents |
| Google Drive | Organize, search, and manage files and folders |
| Google Forms | Create surveys, update questions, and retrieve responses |
| Gmail | Read, send, and manage email content |
| Google Keep | Manage notes, lists, and attachments |
| Google Meet | Schedule and manage video calls |
| Contacts | Sync and manage contacts |
| Google Sheets | Read, write, and format spreadsheet data |
| Google Slides | Create and modify presentations |
| Google Tasks | Create, manage, and organize tasks |

### Authentication and permissions

As the builder, you don't need to configure OAuth clients, manage credentials,
or set up a Google Cloud project. AI Studio handles all of this for you.

Apps with Workspace APIs integrated use "Sign in with Google" to authenticate
end users. When a user opens your app, they are prompted to sign in and grant
the specific permissions your app needs (for example, read-only access to their
calendar, or the ability to edit a spreadsheet). Your app only accesses the
data of the person using it. Each user authorizes access to their own account.

### Example prompts

Here are a few ideas to get started with Workspace integrations:

- *"Build an app that reads my Google Calendar and drafts prep emails in
  Gmail for each meeting."*
- *"Create a tool that takes a Google Doc and generates a 5-slide summary
  presentation in Google Slides."*
- *"Make an expense tracker where I upload a receipt, Gemini extracts the
  details, and it logs a new row in my Google Sheet."*

### Set up OAuth

One key use case for secrets management is to set up OAuth to connect to other
websites or apps. When your prompt includes instructions about connecting to a
3rd party app that requires OAuth authentication, the agent will provide
instructions on how to set up OAuth for that application. These instructions
will include the necessary callback URLs to configure your OAuth Application.
You can also find the callback URLs under **Integrations** in the Settings panel.

## Build multiplayer experiences

The full-stack runtime enables real-time collaboration features.

- **Real-time state**: You can ask the Agent to build features like "a live chat," "a collaborative whiteboard," or "a multiplayer game."
- **Synced sessions**: The server manages the state, allowing multiple users to interact with the same application instance in real-time.

**Example prompt**: \> "Make this a multiplayer game where players can see each
other's cursors."

### Tips for testing multiplayer apps

You can test multiplayer mode in two ways before deploying your app.

1. Open your app in Google AI Studio Build mode in multiple tabs. When developing in Build mode, your app is in a dev container. Opening the app in multiple tabs will let you simulate multiple players using your app.
2. Share the app with others using the **Share** menu at the top right. then use the **Shared URL** from the **Integrations** tab of the **Share** menu to use the app with the players that you've shared your app with.

## Best practices

- **Gemini API calls** : Your `GEMINI_API_KEY` is automatically configured as a server-side secret. Make Gemini API calls from your server-side code using this key. You can view it in the **Secrets** panel.
- **Secrets security**: Always use the Secrets manager for sensitive keys. Never hardcode them in your files.
- **Separation of concerns**: Keep your UI logic in the client-side framework (React/Angular) and your business logic/data handling on the server side.
- **Error handling**: Ensure your server-side code robustly handles errors from external API calls to prevent the app from crashing.

## What's Next?

- [Build Apps in Google AI Studio](https://ai.google.dev/gemini-api/docs/aistudio-build-mode)
- [Deploying from Google AI Studio](https://ai.google.dev/gemini-api/docs/aistudio-deploying)
- [App Gallery](https://aistudio.google.com/apps?source=showcase)