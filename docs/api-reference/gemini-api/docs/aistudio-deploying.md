# Deploying from Google AI Studio

Google AI Studio lets you to deploy your full-stack applications directly
from Build Mode. This provides a fast path from prototype to a
managed, scalable production environment.

## Deployment options

To deploy your application from AI Studio Build Mode, the requirements depend
on the tier you use:

- [**Google Cloud Starter Tier**](https://docs.cloud.google.com/docs/starter-tier): Lets you publish up to 2 full-stack applications without setting up a Google Cloud project or billing account.
- **Standard Deployment**: Requires a Google Cloud project linked to your AI Studio account and billing enabled on that project.

## About the Starter Tier

The Google Cloud Starter Tier provides a streamlined path to deploy
applications to Google Cloud directly from Google AI Studio without setting
up a full Google Cloud environment or billing account.

Each Google AI Studio deployment creates a corresponding service in
Cloud Run. For services deployed in Google AI Studio with Starter
Tier, the following limitations apply:

- You can deploy up to two services.
- Your services are deployed in a [single Cloud Run region](https://docs.cloud.google.com/run/docs/locations).

## Starter Tier deployment steps

After designing your app in Build mode, deploy it with Starter Tier:

1. Click the **Publish** button in the top right corner.
2. Click **Get Started**.
3. Click **Publish App**.

Once deployment is complete, AI Studio provides a Cloud Run URL where you can
access your live application.

> [!NOTE]
> **Note:** If you already have 2 applications deployed, you will be asked to either unpublish one of the applications or upgrade to a paid account. You may also need to upgrade to a paid account if you don't have starter tier available.

## Standard deployment

As your applications evolve, you might require capabilities beyond the Starter
Tier, such as higher quotas or increased compute resources or other
Google Cloud products not available in the Starter Tier. To unlock
these capabilities, you can convert your fully managed Starter Tier project into
a standard Google Cloud project.

This ensures that you can scale seamlessly without losing
your progress. Follow the steps to
[create a Cloud Billing account](https://docs.cloud.google.com/billing/docs/how-to/create-billing-account#create-new-billing-account),
formally accept the standard Google Cloud Terms of Service, and
[upgrade to a standard Google Cloud project](https://docs.cloud.google.com/docs/starter-tier#upgradee).
For more information, see
[Setup for Paid accounts](https://docs.cloud.google.com/billing/docs/in-product-billing-setup#paid-setup).

To learn more about billing tiers, see [Billing](https://ai.google.dev/gemini-api/docs/billing).

## Delete your application

If you no longer need your app, you can delete it in Google AI Studio
by following these instructions:

1. In Google AI Studio, go to your [Apps page](https://aistudio.google.com/app/apps).
2. In the left menu, select **Apps**.
3. Hold the pointer over the app you want to delete.
4. Click the trash can icon on the right-side of the row to delete the app.

## What's next

- Learn more about the [Google Cloud Starter Tier](https://docs.cloud.google.com/docs/starter-tier).
- Read about [Billing](https://ai.google.dev/gemini-api/docs/billing) in Gemini API.